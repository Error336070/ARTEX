package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel implements custom DingTalk bots. Platform details guide these choices: each bot
// permits 20 messages/minute and may silently drop excess messages even with HTTP 200, requiring
// client limits (DefaultRatePerMin). Security options are signing, custom keywords, or IP
// allowlists; signing alone is independent of message content, so only signing and unprotected
// webhooks are supported. Both success and failure return HTTP 200; inspect body errcode to
// distinguish them.
//
//
//
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// DingTalk Webhook URLs contain access_token and are credentials, so mask the whole value.
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// The destination is the DingTalk Webhook URL; changing it requires an explicit signing-secret decision for the new destination.
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Missing Webhook URL")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Invalid Webhook URL: %w", err)
	}
	return nil
}

// Send uses ActionCard with a button for single messages with backlinks, otherwise Markdown.
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// DingTalk has no clear Markdown byte limit; still cap output to guard against unusually large evidence fields.
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "View details",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// DingTalk reports business errors inside HTTP 200 responses.
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("Failed to parse DingTalk response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 means signature validation failure; 310000 means keyword mismatch. Both are configuration
		// errors that retries cannot repair.
		return 0, Permanent(fmt.Errorf("DingTalk returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL appends timestamp/sign using the official signing rules: sign timestamp + "\n"
// + secret with HMAC-SHA256 keyed by secret, then base64 and URL encode. Timestamp is
// milliseconds. An empty secret returns the URL unchanged for bots without signing.
//
//
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// Do not propagate err: url.Parse errors contain the full URL including access_token.
		return "", fmt.Errorf("Failed to parse Webhook URL: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL checks usable URLs, supported schemes, and literal-IP internal addresses. Errors
// must redact secrets: url.Parse returns *url.Error with the complete original URL, including
// access_token, WeCom key, Telegram bot token, or Feishu hook ID. Returning it previously leaked
// credentials into test 400 responses, persisted last_error, server logs, and delivery history.
// Literal IPs are checked immediately for configuration feedback; domain resolution is checked at
// dial time by blockInternalDial, the final guard that covers DNS rebinding. Scheme restrictions
// are defensive: file:///gopher:// could produce unexpected http.Client behaviour and should
// remain excluded.
//
//
//
//
//
//
//
//
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("Cannot parse URL（%s）", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("Only http/https supported; got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("Missing hostname")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("Delivery to loopback/link-local address %s blocked (set %s=1 to allow local services)", ip, AllowLocalTargetsEnv)
	}
	return nil
}
