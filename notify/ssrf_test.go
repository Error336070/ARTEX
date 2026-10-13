package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Tests cover SSRF protections against internal/cloud metadata delivery and credential-free URL
// validation errors. Ordinary tests use loopback httptest receivers, so TestMain enables
// AllowLocalTargetsEnv; SSRF tests clear it explicitly to verify default rejection.
//
//
//
//

func TestMain(m *testing.M) {
	// Allow normal tests to reach local fake receivers; SSRF tests temporarily clear this setting.
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault requires connection-layer rejection of loopback delivery
// by default.
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // Disable the opt-in to test default behaviour.
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("Loopback delivery should be blocked by default")
	}
	if hit {
		t.Fatal("Request reached local service; guard ineffective")
	}
	// Errors explain how to opt in because local SMTP relays are legitimate.
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("Rejection should explain explicit opt-in: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn retains explicitly enabled local postfix/internal relay
// deployments.
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("Explicit opt-in should allow delivery: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // Cloud metadata endpoint, a principal reason for this guard.
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // Unmap IPv4-mapped addresses before checking to prevent bypass.
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s should be rejected", s)
		}
	}
	// RFC1918 is deliberately allowed for common internal Mattermost/SMTP relays. This assertion
	// requires an explicit decision before future private-network blocking can disable legitimate
	// deployments.
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed (private networks are valid delivery destinations)", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets checks early configuration feedback: reject
// blocked literal IPs when saving rather than waiting for delivery failure.
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s should be rejected during configuration", raw)
		}
	}
	// Public and RFC1918 addresses pass; private addresses are left to dial-time checks, which permit them.
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s should pass validation: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials covers the previously missed parse-failure branch.
// url.Parse failures return *url.Error with the original URL. Earlier redaction covered only
// http.Client.Do; file://, gopher://, and ftp:// tests parsed successfully and exercised scheme
// validation instead, falsely suggesting this path was safe.
//
//
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // Invalid percent escape.
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // Non-numeric port.
		"http://[::1?access_token=" + leakProbeToken,                  // Unmatched brackets.
	}
	for _, raw := range cases {
		// First confirm the input truly fails url.Parse; otherwise tests might silently exercise another
		// branch and provide false assurance.
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q should fail parsing to cover the intended branch", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q Expected validation failure", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// Also verify channel-layer wrappers do not disclose the address.
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("Invalid URL should fail validation")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault covers SMTP dial protection. Bare net.Dialer
// previously bypassed the other channels' SSRF protections: metadata/loopback connections could
// expose handshake lines via last_error/history, while refusal/timeout timing revealed ports.
// TestMain enables AllowLocalTargetsEnv for many loopback fake receivers, so this test must
// explicitly clear it; otherwise it passes regardless of the guard, the reason the gap originally
// escaped tests.
//
//
//
//
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // Disable the opt-in to test default behaviour.
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("Loopback email delivery should be blocked by default")
	}
	// The connection must never establish: Control rejects it before EHLO can be sent.
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("SMTP session established; guard ineffective")
	}
	// Errors explain how to opt in for legitimate local postfix relays.
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("Rejection should explain explicit opt-in: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn ensures explicit opt-in still delivers to common
// internal/local SMTP relays.
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("Explicit opt-in should allow local SMTP delivery: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("No EHLO observed; session not established")
	}
}
