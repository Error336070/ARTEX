package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// GetSetting distinguishes missing keys and read failures only by error; both return an empty value. Treating errors as unset would open authInit during database failures and allow unauthenticated password replacement, while authStatus would send users to /setup. These tests close the connection pool to force read errors and require both handlers to fail closed.
func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	// Close the pool so GetSetting returns an error rather than sql.ErrNoRows.
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	s.authStatus(w, httptest.NewRequest("GET", "/api/auth/status", nil))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (treating a read failure as uninitialized sends users to /setup to overwrite the password); body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Initialized *bool `json:"initialized"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err == nil && payload.Initialized != nil {
		t.Fatalf("Read failure must not return initialized; got %v", *payload.Initialized)
	}
}

func TestAuthInitFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (allowing a read failure lets unauthenticated requests overwrite an existing password); body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("Read failure must not issue a token: %s", w.Body.String())
	}
}

func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw string
		wantErr  bool
	}{
		{"empty", "", true},
		{"seven characters", "1234567", true},
		{"eight characters", "12345678", false},
		{"eight Chinese characters counted by characters rather than bytes", "密码密码密码密码", false},
		{"three Chinese characters occupy 9 bytes but count as 3 characters", "密码强", true},
		{"72 bytes", strings.Repeat("a", 72), false},
		{"73 bytes exceed the bcrypt limit", strings.Repeat("a", 73), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePassword(tc.pw); (got != "") != tc.wantErr {
				t.Fatalf("validatePassword(%q)=%q, wantErr=%v", tc.pw, got, tc.wantErr)
			}
		})
	}
}

func TestCheckSetupToken(t *testing.T) {
	secret := strings.Repeat("a1", 32)
	for _, tc := range []struct {
		name, configured, provided string
		wantConfigured, wantValid  bool
	}{
		{"missing configuration", "", secret, false, false},
		{"short configuration", "short", "short", false, false},
		{"missing token", secret, "", true, false},
		{"wrong token", secret, strings.Repeat("b2", 32), true, false},
		{"prefix", secret, secret[:32], true, false},
		{"extra whitespace", secret, secret + " ", true, false},
		{"matching token", secret, secret, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(setupTokenEnv, tc.configured)
			configured, valid := checkSetupToken(tc.provided)
			if configured != tc.wantConfigured || valid != tc.wantValid {
				t.Fatalf("checkSetupToken() = (%v, %v), want (%v, %v)", configured, valid, tc.wantConfigured, tc.wantValid)
			}
		})
	}
}
