package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// webhookChannel provides configurable URLs, methods, headers, and JSON templates. One adapter
// covers Slack / Mattermost / Discord / custom receivers without separate implementations.
//
type webhookChannel struct{}

func (webhookChannel) Kind() string { return KindWebhook }

// Generic Webhooks have no official limit; 0 means unlimited by default, with user limits based on receiver capacity.
func (webhookChannel) DefaultRatePerMin() int { return 0 }

// Mask url and headers because URLs often contain tokens and headers hold authentication. Both
// appear in API responses. Editing one header therefore requires resubmitting the whole group; a
// mask retains current values. This deliberately favours re-entry over exposing credentials in
// browsers.
func (webhookChannel) SecretKeys() []string { return []string{"url", "headers"} }

// url is the destination; changing it requires an explicit headers decision so stored
// Authorization does not follow the new destination and defeat masks.
func (webhookChannel) DestinationKeys() []string { return []string{"url"} }

// webhookDefaultTemplate supplies a simple JSON body for most custom receivers when no template is
// provided.
const webhookDefaultTemplate = `{
  "title": {{json .Title}},
  "batch": {{.Batch}},
  "count": {{.Count}},
  "items": [
{{- range $i, $it := .Items}}
{{- if $i}},{{end}}
    {
      "finding_id": {{$it.FindingID}},
      "name": {{json $it.Name}},
      "vulnclass": {{json $it.VulnClass}},
      "severity": {{json $it.Severity}},
      "summary": {{json $it.Summary}},
      "assets": {{json $it.Assets}},
      "detail_url": {{json $it.DetailURL}}
    }
{{- end}}
  ]
}`

// webhookTemplateData is the context exposed to user templates.
type webhookTemplateData struct {
	Title   string
	Batch   bool
	Count   int
	Items   []webhookItem
	HomeURL string
	// SentAt records this delivery's RFC3339 timestamp for receivers.
	SentAt string
}

type webhookItem struct {
	FindingID     int64
	Name          string
	VulnClass     string
	Severity      string
	SeverityLabel string
	Summary       string
	Assets        []string
	DetailURL     string
	FromStatus    string
	ToStatus      string
	// StatusLabel describes changes such as "Pending → Fixed"; empty for non-status-change events.
	StatusLabel string
}

func (webhookChannel) Validate(cfg map[string]any) error {
	raw := cfgString(cfg, "url")
	if raw == "" {
		return errors.New("Missing destination URL")
	}
	if err := validateHTTPURL(raw); err != nil {
		return fmt.Errorf("Invalid destination URL: %w", err)
	}
	if m := strings.ToUpper(cfgString(cfg, "method")); m != "" && m != http.MethodGet && m != http.MethodPost && m != http.MethodPut && m != http.MethodPatch {
		return fmt.Errorf("Unsupported method %s (use GET/POST/PUT/PATCH)", m)
	}
	if tpl := cfgString(cfg, "body_template"); tpl != "" {
		if _, err := parseWebhookTemplate(tpl); err != nil {
			return fmt.Errorf("Request body template syntax error: %w", err)
		}
	}
	return nil
}

func (c webhookChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	method := strings.ToUpper(cfgString(cfg, "method"))
	if method == "" {
		method = http.MethodPost
	}

	// GET has no body. Query embedding exceeds template capability and GET semantics, so GET suits
	// receivers where a match simply triggers a hook.
	var payload any
	if method != http.MethodGet {
		body, err := renderWebhookBody(cfgString(cfg, "body_template"), m)
		if err != nil {
			return 0, Permanent(err)
		}
		// Convert rendered JSON strings to json.RawMessage for unchanged transmission rather than
		// double-escaping structures into JSON string values.
		if !json.Valid([]byte(body)) {
			return 0, Permanent(errors.New("Request body template output is not valid JSON"))
		}
		payload = json.RawMessage(body)
	}

	headers := cfgMap(cfg, "headers")
	if ct := cfgString(cfg, "content_type"); ct != "" {
		// Allow overrides, applying this after headers so explicit configuration wins.
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = ct
	}
	if _, err := doJSON(ctx, method, cfgString(cfg, "url"), headers, payload); err != nil {
		return 0, err
	}
	// Generic Webhooks do not truncate bodies; receiver services and body_template determine size, so
	// the whole batch counts as delivered.
	return len(m.Items), nil
}

// renderWebhookBody renders requests using the user or default template.
func renderWebhookBody(tpl string, m Message) (string, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = webhookDefaultTemplate
	}
	t, err := parseWebhookTemplate(tpl)
	if err != nil {
		return "", fmt.Errorf("Request body template syntax error: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, newWebhookTemplateData(m)); err != nil {
		return "", fmt.Errorf("Failed to render request body template: %w", err)
	}
	return buf.String(), nil
}

// parseWebhookTemplate parses templates. missingkey=zero renders missing map keys as zero values
// rather than errors; context here is a struct, mainly allowing range over empty .Items. Nil
// .Items is the case to guard.
//
func parseWebhookTemplate(tpl string) (*template.Template, error) {
	return template.New("body").Funcs(webhookTemplateFuncs).Option("missingkey=zero").Parse(tpl)
}

// webhookTemplateFuncs contains helpers exposed to templates.
var webhookTemplateFuncs = template.FuncMap{
	// json serialises arbitrary values. It is essential: direct {{.Title}} interpolation makes bodies
	// invalid JSON when titles contain quotes/newlines, causing rejection with confusing JSON parsing
	// errors.
	//
	//
	"json": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	},
	// jsons embeds JSON fragments inside another JSON string value with one layer of string escaping.
	"jsons": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		quoted, err := json.Marshal(string(raw))
		if err != nil {
			return "", err
		}
		// Remove outer quotes; callers decide whether to add them.
		return string(quoted[1 : len(quoted)-1]), nil
	},
}

func newWebhookTemplateData(m Message) webhookTemplateData {
	d := webhookTemplateData{
		Title:   markdownTitle(m),
		Batch:   m.Batch,
		Count:   len(m.Items),
		HomeURL: m.HomeURL,
		SentAt:  time.Now().Format(time.RFC3339),
		Items:   make([]webhookItem, 0, len(m.Items)),
	}
	for _, it := range m.Items {
		wi := webhookItem{
			FindingID:     it.FindingID,
			Name:          it.Name,
			VulnClass:     it.VulnClass,
			Severity:      it.Severity,
			SeverityLabel: SeverityLabel(it.Severity),
			Summary:       it.Summary,
			Assets:        append([]string{}, it.Assets...),
			DetailURL:     it.DetailURL,
			FromStatus:    it.FromStatus,
			ToStatus:      it.ToStatus,
		}
		if it.IsStatusChange() {
			wi.StatusLabel = StatusLabel(it.FromStatus) + " → " + StatusLabel(it.ToStatus)
		}
		d.Items = append(d.Items, wi)
	}
	return d
}
