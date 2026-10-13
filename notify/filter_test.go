package notify

import "testing"

func TestParseFilterMalformedFallsBackToMatchAll(t *testing.T) {
	// Malformed JSON, empty input, and wrong field types all fall back to zero-value Filter: no
	// filtering. This implements preferring extra delivery to omissions; errors or partial parsing
	// could silently discard all high-severity notifications after a single typo.
	cases := []struct {
		name string
		raw  string
	}{
		{"Empty input", ""},
		{"Invalid JSON", `{not json`},
		{"Truncated JSON", `{"min_severity":`},
		{"Type mismatch", `{"min_severity": 123, "task_ids": "abc"}`},
		{"Top-level array", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ParseFilter([]byte(tc.raw))
			if f.MinSeverity != "" || len(f.TaskIDs) != 0 || len(f.AssetIDs) != 0 {
				t.Fatalf("Malformed configuration should fall back to zero Filter; got %+v", f)
			}
			// Zero-value Filter matches every event.
			ev := Snapshot{Kind: EventFindingCreated, Severity: "low", VulnClass: "XSS"}
			if !Match(f, ev) {
				t.Fatal("Zero Filter should match all events")
			}
		})
	}
}

func TestMatchSeverityThreshold(t *testing.T) {
	ev := func(sev string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: sev}
	}
	cases := []struct {
		min    string
		sev    string
		expect bool
	}{
		{"", "low", true},
		{"", "critical", true},
		{"high", "critical", true},
		{"high", "high", true},
		{"high", "medium", false},
		{"high", "low", false},
		{"critical", "high", false},
		{"critical", "critical", true},
		// Unknown severity has rank 0 and must fail every nonempty threshold; do not send when uncertain.
		{"low", "", false},
		{"low", "unknown", false},
		{"", "", true},
	}
	for _, tc := range cases {
		got := Match(Filter{MinSeverity: tc.min}, ev(tc.sev))
		if got != tc.expect {
			t.Errorf("min=%q sev=%q: Expected %v; got %v", tc.min, tc.sev, tc.expect, got)
		}
	}
}

func TestMatchScopeRestrictions(t *testing.T) {
	ev := Snapshot{
		Kind:      EventFindingCreated,
		Severity:  "high",
		TaskID:    7,
		AssetIDs:  []int64{10, 20},
		VulnClass: "SQL注入",
	}
	cases := []struct {
		name   string
		filter Filter
		expect bool
	}{
		{"Empty scope is unrestricted", Filter{}, true},
		{"Task match", Filter{TaskIDs: []int64{7}}, true},
		{"Task mismatch", Filter{TaskIDs: []int64{8}}, false},
		{"Multiple tasks include match", Filter{TaskIDs: []int64{8, 7}}, true},
		{"Assets intersect", Filter{AssetIDs: []int64{20, 99}}, true},
		{"Assets do not intersect", Filter{AssetIDs: []int64{99}}, false},
		{"Task and asset match", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{10}}, true},
		{"Task match but asset mismatch", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{99}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev); got != tc.expect {
				t.Errorf("Expected %v; got %v", tc.expect, got)
			}
		})
	}
}

func TestMatchVulnClassKeywords(t *testing.T) {
	ev := func(class string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: "high", VulnClass: class}
	}
	cases := []struct {
		name   string
		filter Filter
		class  string
		expect bool
	}{
		{"Empty include accepts all", Filter{}, "任意类型", true},
		{"Include match", Filter{VulnClassInclude: []string{"SQL"}}, "SQL注入", true},
		{"Include mismatch", Filter{VulnClassInclude: []string{"命令执行"}}, "SQL注入", false},
		{"Any include keyword matches", Filter{VulnClassInclude: []string{"命令执行", "SQL"}}, "SQL注入", true},
		{"Case insensitive", Filter{VulnClassInclude: []string{"sql"}}, "SQL注入", true},
		{"Exclude match rejects", Filter{VulnClassExclude: []string{"信息泄露"}}, "信息泄露", false},
		{"Exclude mismatch accepts", Filter{VulnClassExclude: []string{"信息泄露"}}, "SQL注入", true},
		// Exclusions override inclusions when both match.
		{"Exclusion precedes inclusion", Filter{
			VulnClassInclude: []string{"SQL"},
			VulnClassExclude: []string{"注入"},
		}, "SQL注入", false},
		// Ignore whitespace-only keywords to avoid matching every string containing spaces.
		{"Blank keywords ignored", Filter{VulnClassInclude: []string{"", "  "}}, "SQL注入", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev(tc.class)); got != tc.expect {
				t.Errorf("Expected %v; got %v", tc.expect, got)
			}
		})
	}
}

func TestMatchStatusChangeRequiresOptIn(t *testing.T) {
	ev := Snapshot{Kind: EventFindingStatusChanged, Severity: "critical", FromStatus: "pending", ToStatus: "fixed"}
	// Default off: finding notifications normally mean new findings, not status-change logs.
	if Match(Filter{MinSeverity: "low"}, ev) {
		t.Fatal("Status changed events should be skipped when disabled")
	}
	if !Match(Filter{OnStatusChange: true}, ev) {
		t.Fatal("Status changed events should match with on_status_change enabled")
	}
	// Creation events are unaffected by on_status_change.
	created := Snapshot{Kind: EventFindingCreated, Severity: "critical"}
	if !Match(Filter{MinSeverity: "low"}, created) {
		t.Fatal("Creation events should not depend on on_status_change")
	}
}
