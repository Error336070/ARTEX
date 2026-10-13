package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// Global setting keys stored in existing settings key/value table.
const (
	// settingNotifyEnabled is the default-on master switch for maintenance shutdown; actual feature use depends on
	// configured channels.
	settingNotifyEnabled = "notify_enabled"
	// settingNotifyPublicBaseURL is the external finding-detail URL base, e.g. https://artex.example.com. Empty
	// omits callback buttons. Added because no reusable external URL setting existed.
	settingNotifyPublicBaseURL = "notify_public_base_url"
	// settingNotifyDigestMinutes sets digest interval in minutes.
	settingNotifyDigestMinutes = "notify_digest_interval_min"
)

const (
	// notifyTick is delivery polling interval. Three seconds bounds realtime responsiveness and is the main delay
	// between saved findings and IM delivery.
	notifyTick = 3 * time.Second
	// notifyLease must significantly exceed worst-case delivery duration (notify HTTP client timeout 15 seconds),
	// preventing duplicate dispatchers claiming one row.
	notifyLease = 3 * time.Minute
	// notifyFanOutPerTick limits event expansion so newly enabled channels do not expand all historical backlog at
	// once.
	notifyFanOutPerTick = 200
	// notifyDefaultDigestMinutes is the default digest interval.
	notifyDefaultDigestMinutes = 30
	// notifyUnlimitedBurstPerTick caps a round for unlimited channels so thousands of findings cannot block one loop
	// indefinitely.
	notifyUnlimitedBurstPerTick = 50
	// notifyMaxSendsPerChannelPerTick derives from lease duration. Serial requests exceeding the 3-minute lease
	// permit another process to reclaim rows, duplicate sends/attempt increments, and mark still-running deliveries
	// failed. 6 × 30 seconds leaves no margin; 5 gives 150 seconds plus 30 seconds margin.
	// TestNotifyTickBudgetFitsWithinLease checks lease/timeout/count together.
	notifyMaxSendsPerChannelPerTick = 5
	// notifySendTimeout bounds each delivery and determines the previous constant; their product must remain below
	// notifyLease (TestNotifyTickBudgetFitsWithinLease).
	notifySendTimeout = 30 * time.Second
)

// notifyBackoff indexes delays by attempts already made. Three total attempts match db.MaxNotifyAttempts; change both together.
var notifyBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

// Notifier is the finding delivery engine, running independently beside Scheduler (server.New). Separate ticks preserve 3-second responsiveness and isolate delivery failures from agent triggers.
type Notifier struct {
	s  *Server
	pg *db.DB

	// mu protects a small, low-contention channel bucket map; one mutex suffices.
	mu      sync.Mutex
	buckets map[int64]*notifyBucket
}

// notifyBucket is a per-channel token bucket. Constant refill avoids window-boundary bursts where 20 sends before and after reset produce 40 sends in one second and trigger platform limits.
type notifyBucket struct {
	tokens   float64
	lastFill time.Time
}

func newNotifier(s *Server) *Notifier {
	return &Notifier{s: s, pg: s.m.pg, buckets: map[int64]*notifyBucket{}}
}

// Run loops until ctx ends; server.New starts it once.
func (n *Notifier) Run(ctx context.Context) {
	if n.pg == nil {
		return
	}
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.step(ctx)
		}
	}
}

// step dispatches new events then sends due deliveries. Failures only log; notification failures must not become process failures. Independent ticks retry naturally.
func (n *Notifier) step(ctx context.Context) {
	if !n.enabled() {
		return
	}
	if _, _, err := n.pg.FanOutPendingEvents(ctx, notifyFanOutPerTick); err != nil {
		log.Printf("[notify] Failed to dispatch event: %v", err)
		return
	}
	channels, err := n.pg.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("[notify] Failed to read channels: %v", err)
		return
	}
	baseURL := n.publicBaseURL()
	for _, ch := range channels {
		if !ch.IsEnabled() {
			continue
		}
		// Tokens count messages/HTTP requests, not findings. Realtime uses one per finding; a digest batch uses one.
		// Check tokens before claiming so rate-limited deliveries do not consume retries.
		now := time.Now()
		if ch.Mode == db.NotifyModeDigest {
			tokens, claimLimit := digestTickPlan()
			if n.takeTokens(ch.ID, ch.RatePerMin, tokens, now) <= 0 {
				continue
			}
			n.stepDigest(ctx, ch, claimLimit, baseURL)
			continue
		}
		allow := n.takeTokens(ch.ID, ch.RatePerMin, notifyMaxSendsPerChannelPerTick, now)
		if allow <= 0 {
			continue
		}
		n.stepRealtime(ctx, ch, allow, baseURL)
	}
}

// digestTickPlan separates token count (always 1 message/request per digest) from claimLimit (findings per batch, memory-limited). Treating lease-derived request budget as batch size previously reduced rate_per_min=20 at 3-second ticks to one-finding digests, never reaching db.MaxDigestBatchSize. Existing end-to-end tests bypassed that calculation; TestDigestTickPlanDecouplesBatchSizeFromSendBudget verifies it directly.
func digestTickPlan() (tokens, claimLimit int) {
	return 1, db.MaxDigestBatchSize
}

// stepRealtime claims and sends channel deliveries, one message per finding.
func (n *Notifier) stepRealtime(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	deliveries, err := n.pg.ClaimRealtimeDeliveries(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] Failed to claim realtime delivery channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("Channel type %q is not registered", ch.Kind))
		return
	}
	for _, dl := range deliveries {
		msg, err := n.renderSingle(ctx, dl, baseURL)
		if err != nil {
			// Rendering failures are local data problems and cannot improve with retry.
			_ = n.pg.FailDeliveries(ctx, []int64{dl.ID}, err.Error())
			continue
		}
		n.send(ctx, channel, cfg, msg, []*db.NotificationDelivery{dl})
	}
}

// stepDigest aggregates due channel deliveries into one message.
func (n *Notifier) stepDigest(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	window := n.digestInterval()
	due, err := n.pg.DigestBatchDue(ctx, ch.ID, window)
	if err != nil {
		log.Printf("[notify] Failed to evaluate digest batch channel=%d: %v", ch.ID, err)
		return
	}
	if !due {
		return
	}
	deliveries, err := n.pg.ClaimDigestBatch(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] Failed to claim digest batch channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("Channel type %q is not registered", ch.Kind))
		return
	}
	msg, included, err := n.renderBatch(ctx, deliveries, baseURL, int(window.Minutes()))
	if err != nil {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), err.Error())
		return
	}
	// Explicitly fail deliveries with bad snapshots that cannot enter messages. Otherwise excluded records miss bulk
	// marking and remain sending until repeated lease expiry/reclaim.
	if skipped := excludeDeliveries(deliveries, included); len(skipped) > 0 {
		reason := "Event snapshot cannot be parsed; this finding cannot be rendered as a message"
		if fErr := n.pg.FailDeliveries(ctx, deliveryIDs(skipped), reason); fErr != nil {
			log.Printf("[notify] Failed to mark invalid snapshot delivery channel=%s ids=%v: %v", ch.Kind, deliveryIDs(skipped), fErr)
		}
		log.Printf("[notify] Skipped %d deliveries with unparseable snapshots channel=%d", len(skipped), ch.ID)
	}
	// Pass only included entries to send; included[i] must exactly match msg.Items[i] so reported first-K delivery
	// counts map to correct rows.
	n.send(ctx, channel, cfg, msg, included)
}

// send delivers and transitions state. A digest batch shares success/retry outcomes, preserving batch semantics. Channel length limits are the exception: only first K entries succeed; the rest remain for another batch rather than disappearing as falsely delivered.
func (n *Notifier) send(ctx context.Context, channel notify.Channel, cfg map[string]any, msg notify.Message, deliveries []*db.NotificationDelivery) {
	// Bound each send so a stuck channel cannot block remaining channels for the whole round.
	sendCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
	defer cancel()
	delivered, err := channel.Send(sendCtx, cfg, msg)
	if err == nil && delivered > 0 {
		if delivered > len(deliveries) {
			// A reported count exceeding deliveries indicates renderer error; log it and treat all as delivered rather than
			// corrupting records.
			log.Printf("[notify] Channel reported %d delivered entries, exceeding %d deliveries channel=%s; treating all as delivered",
				delivered, len(deliveries), channel.Kind())
			delivered = len(deliveries)
		}
		sent, rest := deliveries[:delivered], deliveries[delivered:]
		if err := n.pg.MarkDeliveriesSent(ctx, deliveryIDs(sent)); err != nil {
			log.Printf("[notify] Failed to mark delivered channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(sent), err)
		}
		if len(rest) > 0 {
			// Length-limit remainder returns immediately for the next tick. DeferDeliveries, not RescheduleDeliveries,
			// restores the optimistic claim attempt increment because this is not failure.
			if err := n.pg.DeferDeliveries(ctx, deliveryIDs(rest),
				fmt.Sprintf("Message reached the channel length limit; only the first %d entries were delivered, with the rest left for the next batch", delivered)); err != nil {
				log.Printf("[notify] Failed to queue remaining segments channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(rest), err)
			}
		}
		return
	}
	if err == nil {
		// No error and no delivery count still means failure with backoff, preventing endlessly reclaimed unmarkable
		// rows.
		err = fmt.Errorf("Channel did not report delivery count (delivered=%d)", delivered)
	}

	// Decide failure per entry, not maximum batch attempts. Previously an old attempts=2 entry could exhaust new
	// attempts=1 entries without allowing any retry, permanently losing new findings.
	permanent := notify.IsPermanent(err)
	var failIDs, exhaustedIDs []int64
	byDelay := map[time.Duration][]int64{}
	for _, dl := range deliveries {
		switch {
		case permanent:
			failIDs = append(failIDs, dl.ID)
		case dl.Attempts >= db.MaxNotifyAttempts:
			exhaustedIDs = append(exhaustedIDs, dl.ID)
		default:
			delay := notifyBackoff[min(dl.Attempts, len(notifyBackoff)-1)]
			byDelay[delay] = append(byDelay[delay], dl.ID)
		}
	}

	if len(failIDs) > 0 {
		if fErr := n.pg.FailDeliveries(ctx, failIDs, err.Error()); fErr != nil {
			log.Printf("[notify] Failed to mark failure status channel=%s ids=%v: %v", channel.Kind(), failIDs, fErr)
		}
	}
	if len(exhaustedIDs) > 0 {
		reason := fmt.Sprintf("Still failed after %d retries: %s", db.MaxNotifyAttempts, err)
		if fErr := n.pg.FailDeliveries(ctx, exhaustedIDs, reason); fErr != nil {
			log.Printf("[notify] Failed to mark failure status channel=%s ids=%v: %v", channel.Kind(), exhaustedIDs, fErr)
		}
	}
	// Group rescheduling by three backoff stages, avoiding one UPDATE per entry and 500 round trips for a 500-entry
	// batch.
	for delay, group := range byDelay {
		if rErr := n.pg.RescheduleDeliveries(ctx, group, delay, err.Error()); rErr != nil {
			log.Printf("[notify] Failed to reschedule delivery channel=%s ids=%v: %v", channel.Kind(), group, rErr)
		}
	}
	if len(failIDs)+len(exhaustedIDs) > 0 {
		log.Printf("[notify] Delivery failed channel=%d kind=%s permanentFailures=%d retriesExhausted=%d pendingRetries=%d: %s",
			deliveries[0].ChannelID, channel.Kind(), len(failIDs), len(exhaustedIDs), len(byDelay), err)
	}
}

// excludeDeliveries compares pointer identity and returns all outside keep. Explicitly handle deliveries omitted from the message.
func excludeDeliveries(all, keep []*db.NotificationDelivery) []*db.NotificationDelivery {
	inKeep := make(map[*db.NotificationDelivery]bool, len(keep))
	for _, dl := range keep {
		inKeep[dl] = true
	}
	var out []*db.NotificationDelivery
	for _, dl := range all {
		if !inKeep[dl] {
			out = append(out, dl)
		}
	}
	return out
}

// adapt resolves channel implementation/config. Unregistered types return ok=false and fail directly rather than retry forever.
func (n *Notifier) adapt(ch *db.NotificationChannel) (notify.Channel, map[string]any, bool) {
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		return nil, nil, false
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		// Malformed configuration becomes an empty map so channel Validate can identify missing fields more helpfully
		// than a JSON error.
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return channel, cfg, true
}

// renderSingle renders one finding message.
func (n *Notifier) renderSingle(ctx context.Context, dl *db.NotificationDelivery, baseURL string) (notify.Message, error) {
	snap, err := parseSnapshot(dl)
	if err != nil {
		return notify.Message{}, err
	}
	item, err := n.itemFor(ctx, snap, baseURL)
	if err != nil {
		return notify.Message{}, err
	}
	return notify.Message{Items: []notify.Item{item}, HomeURL: baseURL}, nil
}

// renderBatch parses each snapshot separately and skips only bad entries. included and msg.Items must correspond one-to-one so first-K successes mark correct rows. Remove skipped entries from included; callers explicitly fail them in stepDigest.
func (n *Notifier) renderBatch(ctx context.Context, deliveries []*db.NotificationDelivery, baseURL string, windowMinutes int) (notify.Message, []*db.NotificationDelivery, error) {
	items := make([]notify.Item, 0, len(deliveries))
	included := make([]*db.NotificationDelivery, 0, len(deliveries))
	for _, dl := range deliveries {
		snap, err := parseSnapshot(dl)
		if err != nil {
			// Bad snapshots enter neither message nor included; caller explicitly fails them rather than hiding them among
			// delivered entries.
			log.Printf("[notify] Skipped unparseable snapshot in digest batch delivery=%d: %v", dl.ID, err)
			continue
		}
		item, err := n.itemFor(ctx, snap, baseURL)
		if err != nil {
			return notify.Message{}, nil, err
		}
		items = append(items, item)
		included = append(included, dl)
	}
	if len(items) == 0 {
		return notify.Message{}, nil, fmt.Errorf("All %d deliveries in digest batch have unparseable snapshots", len(deliveries))
	}
	return notify.Message{
		Items:         items,
		Batch:         true,
		WindowMinutes: windowMinutes,
		HomeURL:       baseURL,
	}, included, nil
}

// itemFor renders an event snapshot, resolving asset names and finding-detail callback URLs.
func (n *Notifier) itemFor(ctx context.Context, snap notify.Snapshot, baseURL string) (notify.Item, error) {
	assets, err := n.pg.NotificationAssetNames(ctx, snap.AssetIDs)
	if err != nil {
		// Asset name lookup failure only omits an asset line and must not block notification.
		log.Printf("[notify] Failed to resolve asset name finding=%d: %v", snap.FindingID, err)
	}
	item := notify.Item{
		FindingID:  snap.FindingID,
		Name:       snap.Name,
		VulnClass:  snap.VulnClass,
		Severity:   snap.Severity,
		Summary:    snap.Summary,
		Assets:     assets,
		FromStatus: snap.FromStatus,
		ToStatus:   snap.ToStatus,
	}
	if baseURL != "" {
		// Detail route is web/src/app/(main)/function/findings/detail/page.tsx; finding id comes from query parameter
		// id.
		item.DetailURL = fmt.Sprintf("%s/function/findings/detail?id=%d", baseURL, snap.FindingID)
	}
	return item, nil
}

// takeTokens consumes at most want and returns actual count. One token is one message/HTTP request: realtime passes message count; digest passes 1. Capacity is ratePerMin with constant refill; <=0 returns a finite large per-round allowance. The want cap preserves unused tokens rather than draining capacity that cannot fit caller limits, even on empty rounds.
func (n *Notifier) takeTokens(channelID int64, ratePerMin, want int, now time.Time) int {
	if want <= 0 {
		return 0
	}
	if ratePerMin <= 0 {
		return min(want, notifyUnlimitedBurstPerTick)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[channelID]
	if b == nil {
		b = &notifyBucket{tokens: float64(ratePerMin), lastFill: now}
		n.buckets[channelID] = b
	}
	// Refill by actual elapsed time at ratePerMin/60 tokens per second.
	if elapsed := now.Sub(b.lastFill).Seconds(); elapsed > 0 {
		b.tokens = minF(float64(ratePerMin), b.tokens+elapsed*float64(ratePerMin)/60)
		b.lastFill = now
	}
	// Add epsilon 1e-9 before truncating: floating-point 0.5+0.5 can yield 0.9999999999 and incorrectly return no
	// token. Epsilon is negligible and cannot allow a real deficit.
	take := min(int(b.tokens+1e-9), want)
	if take <= 0 {
		return 0
	}
	b.tokens -= float64(take)
	return take
}

// enabled reads the master switch.
func (n *Notifier) enabled() bool {
	return n.pg.GetBool(settingNotifyEnabled, true)
}

// publicBaseURL returns the external callback base without trailing slash.
func (n *Notifier) publicBaseURL() string {
	v, ok, err := n.pg.GetSetting(settingNotifyPublicBaseURL)
	if err != nil || !ok {
		return ""
	}
	return trimTrailingSlash(v)
}

// digestInterval resolves configured interval, using default for missing/invalid values.
func (n *Notifier) digestInterval() time.Duration {
	v, ok, err := n.pg.GetSetting(settingNotifyDigestMinutes)
	if err != nil || !ok {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	m := 0
	if _, err := fmt.Sscanf(v, "%d", &m); err != nil || m <= 0 {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// parseSnapshot parses the event snapshot for a delivery.
func parseSnapshot(dl *db.NotificationDelivery) (notify.Snapshot, error) {
	var snap notify.Snapshot
	if len(dl.Snapshot) == 0 {
		return snap, fmt.Errorf("Event snapshot for delivery %d is empty", dl.ID)
	}
	if err := json.Unmarshal(dl.Snapshot, &snap); err != nil {
		return snap, fmt.Errorf("Failed to parse event snapshot for delivery %d: %w", dl.ID, err)
	}
	if snap.Kind == "" {
		// Event row's kind is authoritative; snapshot kind may come from an older version.
		snap.Kind = dl.EventKind
	}
	return snap, nil
}

func deliveryIDs(deliveries []*db.NotificationDelivery) []int64 {
	out := make([]int64, 0, len(deliveries))
	for _, dl := range deliveries {
		out = append(out, dl.ID)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
