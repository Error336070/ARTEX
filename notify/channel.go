package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel adapts a notification channel. Implementations must be stateless: one instance is reused
// concurrently by multiple configurations, and all credentials come from cfg.
type Channel interface {
	// Kind returns the channel type identifier, matching the registry key.
	Kind() string
	// Validate checks required fields and formats when saving configuration. Errors are shown directly
	// to users, so identify missing fields rather than merely saying the configuration is invalid.
	Validate(cfg map[string]any) error
	// Send delivers one message and returns the actual delivered entry count and error. Platform
	// length limits may truncate digests. Marking the whole batch delivered would silently lose
	// omitted findings while history reports success. The caller marks only the first kept entries
	// delivered and leaves the remainder for the next batch. An error means delivery failed;
	// *PermanentError means no retry. Ignore kept on failure.
	//
	//
	//
	//
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin returns the platform's recommended per-minute delivery limit for new
	// instances. Zero means no known limit.
	DefaultRatePerMin() int
	// SecretKeys identifies credential fields to mask in API responses and preserve when updates
	// submit masks. Channel implementations own this knowledge: WeCom's entire Webhook URL is a
	// credential, whereas DingTalk has a secret. Higher layers must not guess.
	//
	SecretKeys() []string
	// DestinationKeys identifies fields deciding where messages go. Like SecretKeys, this is
	// security-sensitive: retaining credentials while changing destinations could send stored secrets
	// to a server controlled by a configuration editor, defeating masking. See PrepareConfigUpdate.
	//
	//
	//
	DestinationKeys() []string
}

// registry explicitly lists channels instead of init() self-registration, keeping supported types
// visible together and exposing omissions at compile time rather than relying on runtime side effects.
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindFeishu:   feishuChannel{},
	KindWeCom:    weComChannel{},
	KindWebhook:  webhookChannel{},
	KindTelegram: telegramChannel{},
	KindEmail:    emailChannel{},
}

// Get returns a channel implementation by type.
func Get(kind string) (Channel, bool) {
	c, ok := registry[kind]
	return c, ok
}

// ValidKind reports whether kind is supported.
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds returns supported types in lexical order for stable UI dropdowns.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError marks non-retryable delivery failures, including bad credentials, destination
// rejection, and invalid bodies. Retries help only transient failures (network instability,
// limits, remote 5xx); retrying permanent failures cannot succeed and buries the real cause in logs.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marks err as permanent; nil returns nil, allowing `return Permanent(someCheck())`.
//
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether the error chain contains a permanent marker.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// Configuration reading helpers. JSONB channel settings decode through encoding/json into
// map[string]any, with float64 numbers and []any arrays. Helpers standardise conversion and
// tolerate UI type variations such as string ports. cfgString trims surrounding whitespace often
// introduced by form copy/paste.
//

//
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt reads integers from float64 (JSON default) or strings.
func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// cfgBool reads booleans, also accepting strings "true"/"1".
func cfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

// cfgStrings reads string arrays, trimming whitespace and dropping empty values.
func cfgStrings(cfg map[string]any, key string) []string {
	raw, ok := cfg[key].([]any)
	if !ok {
		// Also accepts a single string for forms containing one value.
		if s := cfgString(cfg, key); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap reads string maps such as custom HTTP headers, trimming keys/values and dropping empty keys.
func cfgMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
