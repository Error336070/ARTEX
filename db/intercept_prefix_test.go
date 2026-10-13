package db

import "testing"

func TestInterceptSourcePrefixes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ruleID int64
		reason string
		want   string
	}{
		{"historical model", 0, "[模型] fixture", "model"},
		{"English model", 0, "[Model] fixture", "model"},
		{"historical rule precedence", 1, "[模型] fixture", "rule"},
		{"English rule precedence", 1, "[Model] fixture", "rule"},
		{"ordinary reason", 0, "fixture", "unknown"},
		{"embedded prefix", 0, "fixture [Model]", "unknown"},
		{"different case", 0, "[model] fixture", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := interceptSource(tc.ruleID, tc.reason); got != tc.want {
				t.Fatalf("source = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestModelApprovalPrefixes(t *testing.T) {
	dsn := testDSN(t)
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	scope := "approval-prefix-" + t.Name()
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, scope) })
	var ids []int64
	for _, reason := range []string{"[模型] fixture", "[Model] fixture"} {
		id, err := d.CreateDecidedIntercept(0, 0, scope, "test", "Write", []byte(`{"path":"fixture"}`), "denied", reason)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		var source string
		if err := d.QueryRow(`SELECT decision_source FROM intercept_pending WHERE id=$1`, id).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != "model" {
			t.Fatalf("new reason %q stored source %q", reason, source)
		}
		// Missing source reproduces legacy rows handled by list queries and startup backfill.
		if _, err := d.Exec(`UPDATE intercept_pending SET decision_source='' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := d.ListTaskInterceptsPage(scope, 1, 20, InterceptApprovalFilter{DecisionSource: "model"})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("dual-prefix legacy filter: total=%d rows=%d err=%v", total, len(rows), err)
	}
	for _, row := range rows {
		if row.DecisionSource != "model" {
			t.Fatalf("legacy row source = %q", row.DecisionSource)
		}
	}
	// Explicit historical classifications remain authoritative, even with an English model prefix.
	explicitID, err := d.CreateDecidedIntercept(0, 0, scope, "test", "Write", []byte(`{"path":"fixture"}`), "denied", "[Model] explicit fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE intercept_pending SET decision_source='unknown' WHERE id=$1`, explicitID); err != nil {
		t.Fatal(err)
	}
	// Reopen applies the real schema backfill rather than duplicating its SQL in the test.
	d2, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	for _, id := range ids {
		var source string
		if err := d2.QueryRow(`SELECT decision_source FROM intercept_pending WHERE id=$1`, id).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != "model" {
			t.Fatalf("backfilled source = %q, want model", source)
		}
	}
	var source string
	if err := d2.QueryRow(`SELECT decision_source FROM intercept_pending WHERE id=$1`, explicitID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "unknown" {
		t.Fatalf("explicit source changed to %q", source)
	}
}
