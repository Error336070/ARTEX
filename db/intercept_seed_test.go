package db

import (
	"regexp"
	"testing"
)

// The built-in deletion endpoint rule matches complete tool_input JSON, so fixtures use
// JSON directly, matching the Interceptor subject.
func TestDeleteEndpointPathPattern(t *testing.T) {
	re := regexp.MustCompile(deleteEndpointPathPattern)

	hit := []string{
		`{"command":"curl -s 'http://t.com/api/user/delete?id=1'"}`,    // GET to a deletion endpoint.
		`{"command":"curl -X POST http://t.com/admin/delete -d id=1"}`, // POST to a deletion endpoint.
		`{"command":"curl 'http://t.com/api/deleteAll'"}`,
		`{"command":"curl 'http://t.com/api/delete_user?id=1'"}`,
		`{"command":"curl 'http://t.com/api/delete-user?id=1'"}`,
		`{"url":"http://t.com/api/remove?id=1"}`,
		`{"command":"curl http://t.com/files/unlink/3"}`,
		`{"command":"curl http://t.com/api/del?id=2"}`,
		`{"command":"curl -X POST http://t/v1/erase"}`,
		`{"command":"curl http://t/admin/destroyAll"}`, // The v1 path rule lacks suffix matching; cover it here.
	}
	for _, s := range hit {
		if !re.MatchString(s) {
			t.Errorf("Expected a match but allowed: %s", s)
		}
	}

	// Require a delimiter after the verb to avoid false positives on read-only /delivery or /details.
	miss := []string{
		`{"command":"curl 'http://t.com/api/delivery?id=1'"}`,
		`{"command":"curl 'http://t.com/order/details'"}`,
		`{"command":"curl 'http://t.com/api/delta/sync'"}`,
		`{"command":"curl 'http://t.com/user/delegate'"}`,
		`{"command":"curl 'http://delete.example.com/'"}`, // Deletion verb in the domain rather than the path.
		`{"command":"curl 'http://t.com/remote/status'"}`,
		`{"command":"nmap -p80 10.0.0.1"}`,
	}
	for _, s := range miss {
		if re.MatchString(s) {
			t.Errorf("Unexpected interception: %s", s)
		}
	}
}
