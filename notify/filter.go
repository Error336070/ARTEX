package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter defines notification_channels.filter JSONB. All fields are optional; omission means no
// filtering, also the malformed-configuration fallback (ParseFilter).
type Filter struct {
	// MinSeverity is the low/medium/high/critical threshold; empty disables it.
	MinSeverity string `json:"min_severity"`
	// Empty TaskIDs / AssetIDs impose no restriction; otherwise events must intersect them.
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// Empty VulnClassInclude accepts all; otherwise vulnclass must contain a keyword. Any
	// VulnClassExclude match rejects, taking priority. Case-insensitive substrings avoid unsafe regex
	// misconfiguration silently disabling channels.
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange controls finding status-change events, meaningful only for realtime mode.
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter never returns errors. Malformed configuration falls back to zero-value Filter,
// matching everything: extra notifications are preferable to silently missing high-severity
// findings. Parse failure must not create an apparently configured channel that sends nothing.
//
//
//
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// Parse failure leaves zero-value f, meaning no filtering.
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity reports valid thresholds; empty means no threshold.
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate checks constrained filter values when saving channels. Unknown thresholds make Match
// evaluate rank >= 0, always true: typos such as "hgih" silently send everything. Although this
// avoids missing findings, it misleads users expecting severity filters without indicating bad
// configuration. Reject at entry. Validate is write-only; tolerant ParseFilter reads keep
// historical bad values from disabling entire channels.
//
//
//
//
//
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("Invalid minimum severity %q; use low / medium / high / critical, or leave empty for no limit", f.MinSeverity)
	}
	return nil
}

// Match decides whether to deliver an event. Like ParseFilter, it never returns errors and treats
// internal exceptions as matches. Order: event type → severity → task/asset scope → finding-type
// keywords.
//
func Match(f Filter, s Snapshot) bool {
	// Status changes require explicit opt-in, off by default; most users expect notifications for
	// newly discovered findings rather than every status transition.
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// Exclusions take priority even when inclusion keywords also match.
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// Linear scans suit these small, manually selected sets of a few dozen; maps cost more than they
	// save.
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold reports whether s contains any keyword, ignoring case.
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
