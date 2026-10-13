package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Tests enforce Webhook template capability boundaries, the only place this package evaluates user
// strings as code. Adding context methods or readFile to FuncMap could silently expand
// capabilities through an innocuous-looking diff. TestTemplateContextHasNoMethods is central:
// text/template invokes exported methods through {{.Foo}}, so any reachable type with methods
// exposes them. Context deliberately contains exported data fields only. Failure means
// webhookTemplateData / webhookItem acquired methods; review possible unintended reads before

// allowing them.
//
//
//
//
//
//
//
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s exposes %d methods (%s): text/template can call them, "+
				"exposing those capabilities to template authors", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal constrains capabilities. Only json/jsons serialisation is exposed;
// no file reads, requests, or command execution.
//
//
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Template function set changed: got %v, expected %v. New functions must not expand capabilities"+
			" (no file access, network requests or command execution)", got, want)
	}
}

// TestTemplateCannotReachUnknownData requires out-of-range/missing template access to fail without
// exposing internal data in errors.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("Access to nonexistent fields should fail")
	}
	// Errors must not contain actual context finding titles/summaries.
	for _, leak := range []string{"SQL注入", "参数 id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("Template error leaked message content %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently treats invalid templates as configuration failures; retrying
// would waste three backoff rounds on every delivery.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("Template syntax errors should be rejected on save")
	}
	// Even direct delivery without validation must mark invalid templates permanent rather than retry repeatedly.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Invalid template should fail permanently; got %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON enforces valid JSON output and prevents plain-text templates
// triggering other protocols.
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// Valid templates pass.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("Valid template should pass validation: %v", err)
	}
	// Reject non-JSON output rather than transmitting it unchanged.
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Non-JSON output should fail permanently; got %v", err)
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Errorf("Error should identify JSON issue; got %v", err)
	}
}
