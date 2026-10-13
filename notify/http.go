package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets controls loopback/link-local delivery, denied by default. Such addresses can
// expose local admin services or cloud metadata (169.254.169.254, containing instance
// credentials). Destinations are admin-configured, but compromised XSS/CSRF sessions or another
// holder of the same JWT could retrieve response excerpts: doJSON places the first 200 bytes of
// 4xx/5xx bodies in last_error/history. Local SMTP relays such as postfix at 127.0.0.1:25 are
// legitimate, so ARTEX_NOTIFY_ALLOW_LOCAL=1 explicitly enables them. Export AllowLocalTargetsEnv
// so this package and server tests can opt in for loopback httptest receivers.
//
//
//
//
//
//
//
//
const AllowLocalTargetsEnv = "ARTEX_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP identifies destinations denied by default: loopback, link-local including cloud
// metadata, unspecified, and multicast. RFC1918 remains allowed for legitimate internal
// Mattermost/SMTP relays. This deliberate choice protects sensitive destinations while retaining
// normal deployments.
//
//
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Unmap IPv4-mapped IPv6 (::ffff:127.0.0.1) before checking to prevent bypass.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial is the http.Transport dialer's Control hook, checking addresses at connection
// establishment. This is the final enforcement point for DNS rebinding after configuration
// validation and redirects (cross-host redirects are already forbidden, but same-host paths can
// still change).
//
//
//
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("Cannot parse destination address %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("Delivery to loopback/link-local address %s blocked (set %s=1 to allow local services)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport adds only a dial guard to a cloned default Transport, preserving pooling,
// HTTP/2, timeouts, proxies, and other tuning.
//
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient is shared by all delivery channels. It deliberately does not reuse server
// GlobalProxy, intended for target traffic and often an unstable tunnel; notification availability
// must remain independent. Messaging uses direct connections and a 15-second timeout. Reject
// cross-host redirects because endpoints are fixed and credentials appear in URLs (DingTalk
// access_token, WeCom key, Telegram token), so following redirects would disclose them. Same-host
// redirects such as trailing slashes remain allowed.
//
//
//
//
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("Too many redirects")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("Cross-host redirect blocked (%s → %s)", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit bounds oversized peer responses; only codes and short descriptions are needed for
// delivery history.
const respBodyLimit = 8 << 10

// doJSON sends a request and returns a bounded response body. Nil payload sends an empty body for
// GET or platforms requiring none. headers are added unchanged for custom Webhooks. Classifying
// failures is central: network errors and 5xx/408/429 are retryable; other 4xx are permanent,
// since retrying 403 only repeats the same log error.
//
//
//
func doJSON(ctx context.Context, method, url string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// Serialisation failure is a local bug (wrong configuration types); retries cannot help.
			return nil, Permanent(fmt.Errorf("Failed to construct request body: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		// Invalid URLs usually mean user configuration errors and are permanent. Do not propagate
		// url.Parse errors containing the full address.
		return nil, Permanent(fmt.Errorf("Invalid request URL: %s", redactRequestTarget(url)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Connection refusal, DNS errors, and timeouts are usually transient and use backoff. Redact
		// errors before propagation: http.Client.Do returns *url.Error formatted as Op "fullURL": cause,
		// and URLs contain DingTalk access_token, WeCom key, Feishu hook ID, or Telegram /bot<token>/.
		// Without redaction, credentials reach persisted notification_deliveries.last_error, history
		// responses bypassing masks, server logs, and frontend test-send 502 responses.
		//
		//
		//
		return nil, fmt.Errorf("Request failed: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("Failed to read response: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// Retry 429 rate limits and 408 timeouts; other 4xx indicate configuration/permission errors.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("Remote rate limit or timeout (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("Remote service error (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("Remote request rejected (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet makes response bodies short single-line errors; newlines and excess whitespace would
// break last_error history layouts.
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget reduces addresses to scheme://host/… for errors. This package consistently
// discards everything except scheme/host because credentials can occur in query (DingTalk
// /robot/send?access_token=xxx, WeCom /cgi-bin/webhook/send?key=xxx), final path (Feishu
// /open-apis/bot/v2/hook/<hook_id>), or mid-path (Telegram /bot<token>/sendMessage). Per-channel
// retention risks missing a credential. Host identifies DNS/connection/certificate issues; masked
// suffixes identify bots. Parse failures return a fixed placeholder, never the original value.
//
//
//
//
//
//
//
//
//
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(Unparseable URL)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError removes addresses and retains causes. *url.Error contains {Op, URL, Err};
// read Err directly rather than Error(), which includes URL. This is more reliable than replacing
// encoded/escaped URL variants afterward.
//
//
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: Unknown error", uerr.Op, host)
	}
	// Other errors, including redirect-policy errors, can also contain URLs and require redaction.
	return redactURLsInText(err.Error())
}

// redactURLsInText replaces http(s) URLs with redacted forms for errors lacking structured fields,
// such as redirect or third-party errors. Recognise http/https and split at whitespace/quotes,
// neither of which occurs inside URLs.
//
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
