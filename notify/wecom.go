package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit is WeCom's hard Markdown content limit in bytes, not characters. It is the
// tightest of six channels and a main reason for TruncateBytes.
const weComMarkdownLimit = 4096

// weComChannel implements WeCom group bots. Authentication uses only URL key, without signing,
// making the whole Webhook URL the credential. Markdown is capped at 4096 bytes and rejected
// entirely if oversized; Chinese characters commonly use three bytes, requiring client truncation
// at roughly a thousand characters. The 20-message/minute limit also needs client enforcement.
//
//
//
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// WeCom's sole credential is the Webhook URL key, with no signing; mask the whole URL and no other
// fields.
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// WeCom's Webhook field is both destination and credential, so no separate stored credentials remain after destination changes.
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Missing Webhook URL")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Invalid Webhook URL: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// Digests of 50 lines plus prefix easily exceed 4096 bytes. Truncate locally rather than relying
	// on rejection, retaining some deliveries instead of losing the whole batch.
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("Failed to parse WeCom response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 means API rate limit; rolling platform windows make backoff useful, so classify it
		// retryable. It indicates an overly aggressive rate_per_min; retries are fallback, and lowering
		// the channel limit is the actual remedy.
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("WeCom rate limit %d: %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 means invalid Webhook key, a permanent failure that retries cannot repair.
		return 0, Permanent(fmt.Errorf("WeCom returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
