package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix marks API credential masks and tells updates to retain stored values. A prefix
// rather than an empty/fixed value allows identification hints (MaskedValue) so users recognise
// bots without repasting secrets.
//
//
const MaskedPrefix = "__masked__"

// MaskedValue returns "__masked__" for short secrets or "__masked__:…ab12cd" with the last six
// characters. Webhook identity usually lives at the end (WeCom key, Feishu bot ID), while shared
// prefixes are unhelpful. Six characters identify a bot without reconstructing credentials.
//
//
//
//
//
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked reports whether a returned masked value is unchanged.
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig copies configuration and masks credentials. Unknown channel types return an empty map
// instead of potentially exposing raw credentials. Non-secret fields remain unchanged for UI
// display.
//
//
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// Treat nested structures such as headers as one credential. Per-child credential rules for every
		// channel would add excessive complexity.
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials means a changed destination lacks explicit credential
// decisions. Reject rather than silently allowing or discarding secrets; see PrepareConfigUpdate.
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // Changed destination keys.
	Missing []string // Credential keys without explicit decisions.
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "Destination (" + strings.Join(e.Changed, ", ") + ") changed; enter credentials again for fields (" +
		strings.Join(e.Missing, ", ") + "): enter new values or explicitly leave empty if credentials are no longer needed. " +
		"The original credentials apply to the old destination; reusing them sends them to the new destination."
}

// PrepareConfigUpdate merges settings and protects destination changes, replacing plain
// MergeConfig on updates. Destination and identity are separate; preserving omitted credentials
// while changing Webhook URL, Telegram base_url, or SMTP host could send stored headers, bot
// tokens, or SMTP credentials to another endpoint, silently and without redirects. Any changed
// destination requires every credential field to be explicit: a new value uses it; an empty string
// clears it; masks and omitted fields are rejected. A mask means reusing old credentials, valid
// only for the old destination. Do not silently discard optional headers/passwords, which could
// lose authentication while still returning 200; require the user to decide.
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("Channel type %q is not registered", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// Reject mask sentinels embedded in non-string credentials such as webhook.headers. MergeConfig
	// recognises only whole prefixed strings, so nested "__masked__" would be stored literally and
	// silently break authentication. Run this check first: unchanged destinations return early, and
	// placing it later leaves that path unprotected, as the initial test exposed.
	//
	//
	//
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// Find destination keys actually changed; masks mean unchanged.
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// Unchanged destinations use normal merge: masks retain stored values, empty strings clear, others replace.
		return MergeConfig(stored, incoming), nil
	}

	// Changed destinations require explicit decisions for every credential field.
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers rejects mask sentinels inside non-string structures. Object credentials
// must be masked as the whole string "__masked__" or submitted fully; embedded sentinels cannot
// express unchanged state and would be stored as real data.
//
//
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("Field %s contains mask marker %q: preserve the whole field or submit a complete replacement; do not embed mask placeholders in containers",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue compares via JSON to tolerate types such as number/int/float64. Normalise blanks
// first: MergeConfig deletes empty-string keys, so blank and absent are equivalent. Otherwise
// Telegram's optional base_url could be saved as "", deleted on first save, then falsely treated
// as changed on the second, causing 400 with a masked token despite no user change and requiring
// repasting the token forever.
//
//
//
//
//
//
//
//
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue must match MergeConfig clearing (strings.TrimSpace(s) == "") so merge and
// equality agree.
//
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig overlays incoming on stored: masks preserve originals, empty strings delete keys,
// other values replace, and omitted keys remain. Empty form values explicitly clear fields rather
// than being stored as meaningful values. This lets users remove incorrect settings; omission
// remains distinct from submitted empty values, although forms do not use that distinction.
//
//
//
//
//
//
//
//
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // Masked means unchanged; retain stored value.
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}
