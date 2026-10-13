package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Tests cover doJSON HTTP error classification separately from adapters' platform codes (DingTalk
// errcode, Feishu code, Telegram ok). These are independent safeguards: incorrect HTTP
// classification could make 503 permanent or waste three retries on 403.
//
//
//

func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoJSONClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"200 success", 200, false},
		{"429 retryable rate limit", 429, false},
		{"408 retryable timeout", 408, false},
		{"500 retryable server error", 500, false},
		{"502 retryable gateway error", 502, false},
		{"503 retryable unavailable service", 503, false},
		{"400 permanent parameter error", 400, true},
		{"401 permanent authentication failure", 401, true},
		{"403 permanent forbidden", 403, true},
		{"404 permanent missing URL", 404, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := replyServer(t, tc.status, `{"detail":"upstream says no"}`)
			_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
			if tc.status < 300 {
				if err != nil {
					t.Fatalf("2xx should succeed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Non-2xx should fail")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("HTTP %d: incorrect permanent classification: expected %v; got %v (%v)",
					tc.status, tc.permanent, got, err)
			}
			// Include status codes so users can distinguish configuration errors from peer failures. Assert
			// numeric codes rather than Go StatusText because numbers remain stable across message languages.
			//
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Errorf("Error should include HTTP status %d; got %v", tc.status, err)
			}
		})
	}
}

// TestDoJSONIncludesResponseSnippet ensures peer rejection explanations are returned rather than
// reporting only failure.
func TestDoJSONIncludesResponseSnippet(t *testing.T) {
	srv := replyServer(t, 400, `{"error":"invalid webhook token"}`)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	if !strings.Contains(err.Error(), "invalid webhook token") {
		t.Errorf("Error should include remote explanation; got %v", err)
	}
}

// TestDoJSONSnippetIsSingleLineAndBounded constrains response text persisted in last_error and
// frontend tables; multiline/oversized output breaks layout and payload size.
func TestDoJSONSnippetIsSingleLineAndBounded(t *testing.T) {
	// Response with newlines, tabs, and 5000 characters.
	long := strings.Repeat("x", 5000)
	srv := replyServer(t, 500, "line1\nline2\r\n\tline3 "+long)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("Error should be one line; got %q", msg)
	}
	// The 200-character snippet plus fixed prefix must be much smaller than the response.
	if len(msg) > 400 {
		t.Errorf("Error too long (%d bytes); snippet should truncate it: %q", len(msg), msg)
	}
}

// TestDoJSONRejectsOversizedResponse ensures large peer bodies are not fully loaded into memory,
// since every delivery stores a last_error copy.
func TestDoJSONRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("A", 1<<20) // 1 MiB
	srv := replyServer(t, 400, huge)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("Expected an error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("Oversized response should be bounded and truncated; error length %d", len(err.Error()))
	}
}
