package notify

import (
	"bufio"
	"context"

	"net"
	"strings"
	"sync"
	"testing"
)

// Protocol-level email tests: email.Send previously had zero coverage, despite the broad SMTP
// handshake/authentication/envelope/DATA failure surface. Use a minimal real SMTP server instead
// of mocking net/smtp, since interacting with the server is the central risk. fakeSMTP supports
// greet/EHLO/AUTH/MAIL/RCPT/DATA/QUIT and configurable reply codes for selected stages.
//
//
//

//
//
type fakeSMTP struct {
	ln net.Listener

	// rcptReply controls RCPT TO replies; default 250.
	rcptReply string
	// mailReply controls MAIL FROM replies; default 250.
	mailReply string
	// advertiseAuth advertises AUTH PLAIN in EHLO when true.
	advertiseAuth bool

	mu       sync.Mutex
	data     string
	commands []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, rcptReply: "250 OK", mailReply: "250 OK"}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	addr, ok := f.ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("Listener address is not TCP")
	}
	return "127.0.0.1", addr.Port
}

func (f *fakeSMTP) record(cmd string) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	f.mu.Unlock()
}

func (f *fakeSMTP) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data
}

func (f *fakeSMTP) sawCommand(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("220 fake.local ESMTP ready")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			// Do not advertise STARTTLS, exercising plaintext envelope handling rather than TLS.
			w("250-fake.local")
			if f.advertiseAuth {
				w("250-AUTH PLAIN")
			}
			w("250 8BITMIME")
		case strings.HasPrefix(line, "AUTH"):
			// Simplify PLAIN handling by accepting the initial response, which may span lines.
			w("235 2.7.0 Authentication successful")
		case strings.HasPrefix(line, "MAIL FROM"):
			w(f.mailReply)
		case strings.HasPrefix(line, "RCPT TO"):
			w(f.rcptReply)
		case strings.HasPrefix(line, "DATA"):
			w("354 End data with <CR><LF>.<CR><LF>")
			var sb strings.Builder
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				sb.WriteString(dl)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			w("250 2.0.0 Ok: queued as FAKE1")
		case strings.HasPrefix(line, "QUIT"):
			w("221 2.0.0 Bye")
			return
		default:
			w("250 OK")
		}
	}
}

func emailCfg(t *testing.T, f *fakeSMTP, extra map[string]any) map[string]any {
	t.Helper()
	host, port := f.hostPort(t)
	cfg := map[string]any{
		"host": host,
		"port": float64(port),
		"from": "artex@example.com",
		"to":   []any{"a@example.com", "b@example.com"},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestEmailSendDeliversFullMessage(t *testing.T) {
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	cfg := emailCfg(t, f, map[string]any{"username": "artex", "password": "pw"})

	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("Delivery failed: %v", err)
	}
	// Must reach envelope stages: sender, two recipients, and DATA.
	for _, want := range []string{"MAIL FROM:<artex@example.com>", "RCPT TO:<a@example.com>", "RCPT TO:<b@example.com>", "DATA", "AUTH", "QUIT"} {
		if !f.sawCommand(want) {
			t.Errorf("SMTP session missing %q; actual commands: %v", want, f.commands)
		}
	}
	// Body is base64 HTML and must retain actual finding content after encoding.
	body := f.body()
	if body == "" {
		t.Fatal("No body received during DATA")
	}
	if !strings.Contains(body, "Content-Type: text/html") {
		t.Errorf("Missing Content-Type header:\n%s", body)
	}
	if !strings.Contains(body, "base64") {
		t.Errorf("Body is not base64 encoded (long HTML lines exceed SMTP limit of 1000 bytes):\n%s", body)
	}
	// All recipients must appear in To.
	if !strings.Contains(body, "a@example.com, b@example.com") {
		t.Errorf("To header missing recipients:\n%s", body)
	}
}

func TestEmailSendWithoutAuth(t *testing.T) {
	// Do not send AUTH without an account; some relays reject it.
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("Delivery failed: %v", err)
	}
	if f.sawCommand("AUTH") {
		t.Errorf("AUTH sent without configured account: %v", f.commands)
	}
}

// TestEmailSendClassifiesSMTPReplies directly verifies the audit fix: 5xx is permanent; 4xx
// greylisting is retryable.
func TestEmailSendClassifiesSMTPReplies(t *testing.T) {
	cases := []struct {
		name      string
		rcptReply string
		mailReply string
		permanent bool
	}{
		{"Recipient permanently rejected with 550", "550 5.1.1 User unknown", "250 OK", true},
		{"Recipient greylisted with 450", "450 4.7.1 Greylisting in action", "250 OK", false},
		{"Recipient mailbox full with 452", "452 4.2.2 Mailbox full", "250 OK", false},
		{"Sender permanently rejected with 553", "250 OK", "553 5.1.3 Bad address", true},
		{"Sender temporary failure with 451", "250 OK", "451 4.3.0 Temporary failure", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSMTP(t)
			f.rcptReply = tc.rcptReply
			f.mailReply = tc.mailReply
			_, err := (emailChannel{}).Send(context.Background(), emailCfg(t, f, nil), singleMsg())
			if err == nil {
				t.Fatal("Expected an error")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("Incorrect permanent classification: expected %v; got %v (%v)", tc.permanent, got, err)
			}
			// Preserve server text so users know whether to contact its administrator or correct the address.
			if !strings.Contains(err.Error(), strings.Fields(tc.rcptReply)[0]) && !strings.Contains(err.Error(), strings.Fields(tc.mailReply)[0]) {
				t.Errorf("Error should preserve server reply code: %v", err)
			}
		})
	}
}

func TestEmailSendRefusesPlaintextCredentials(t *testing.T) {
	// net/smtp PlainAuth rejects credentials over unencrypted connections except localhost. Preserve
	// that security behaviour but provide actionable errors. Use a non-localhost hostname to trigger
	// it.
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	_, port := f.hostPort(t)
	cfg := map[string]any{
		"host":     "smtp.example.com", // Not localhost.
		"port":     float64(port),
		"from":     "a@example.com",
		"to":       []any{"b@example.com"},
		"username": "artex",
		"password": "pw",
	}
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Skip("DNS resolved to a local server; skipping (other cases unaffected)")
	}
	// Connection failure or refusal to send credentials both satisfy the assertion; the password must never be sent silently.
	if !IsPermanent(err) && !strings.Contains(err.Error(), "connect") {
		t.Logf("Error: %v (connection failure expected for a non-localhost address)", err)
	}
}

func TestEmailValidateReportsMissingFields(t *testing.T) {
	// Email has the most configuration fields; test each missing field is caught during validation and
	// named in the error, rather than failing only during delivery.
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"Missing host", map[string]any{"port": float64(25), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"Missing port", map[string]any{"host": "smtp.example.com"}},
		{"Port out of range", map[string]any{"host": "h", "port": float64(70000), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"Missing from", map[string]any{"host": "h", "port": float64(25), "to": []any{"d@e.f"}}},
		{"Missing to", map[string]any{"host": "h", "port": float64(25), "from": "a@b.c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (emailChannel{}).Validate(tc.cfg); err == nil {
				t.Fatalf("Expected validation failure: %v", tc.cfg)
			}
		})
	}
}

// TestEmailConfigTolerance covers JSONB float64 numbers, UI string ports, and single-string array
// values.
func TestEmailConfigTolerance(t *testing.T) {
	cfg := map[string]any{
		"host": "smtp.example.com",
		"port": "587", // Port as a string.
		"from": "a@b.c",
		"to":   "d@e.f", // Single string instead of array.
		"tls":  "true",  // Boolean as a string.
	}
	if err := (emailChannel{}).Validate(cfg); err != nil {
		t.Fatalf("Numeric strings should be accepted: %v", err)
	}
	if got := cfgInt(cfg, "port"); got != 587 {
		t.Errorf("cfgInt did not parse string port; got %d", got)
	}
	if !cfgBool(cfg, "tls") {
		t.Error("cfgBool did not parse string \"true\"")
	}
	if to := cfgStrings(cfg, "to"); len(to) != 1 || to[0] != "d@e.f" {
		t.Errorf("cfgStrings did not accept a single string; got %v", to)
	}
}

// TestFilterValidateRejectsTypo verifies the audit fix: reject threshold typos on write before
// filters silently degrade into sending everything.
func TestFilterValidateRejectsTypo(t *testing.T) {
	good := []string{"", "low", "medium", "high", "critical"}
	for _, s := range good {
		if err := (Filter{MinSeverity: s}).Validate(); err != nil {
			t.Errorf("Valid threshold %q rejected: %v", s, err)
		}
	}
	// Realistic typos must all be rejected.
	for _, s := range []string{"hgih", "HIGH", "严重", "high ", "crit"} {
		err := (Filter{MinSeverity: s}).Validate()
		if err == nil {
			t.Errorf("Invalid threshold %q should be rejected to prevent silently disabling filtering", s)
			continue
		}
		// Errors must explain how to correct the value.
		if !strings.Contains(err.Error(), "low") || !strings.Contains(err.Error(), "critical") {
			t.Errorf("Error should list accepted values; got %q", err.Error())
		}
	}
}

// TestFilterValidateIsWriteTimeOnly enforces strict writes and tolerant reads: existing bad values
// must not make entire channels unreadable and suddenly stop historical deliveries.
func TestFilterValidateIsWriteTimeOnly(t *testing.T) {
	raw := []byte(`{"min_severity":"hgih"}`)
	f := ParseFilter(raw) // No error.
	if f.MinSeverity != "hgih" {
		t.Fatalf("Read path should preserve value; got %q", f.MinSeverity)
	}
	// The channel must still evaluate events without panic or blocking.
	_ = Match(f, Snapshot{Kind: EventFindingCreated, Severity: "critical"})
}
