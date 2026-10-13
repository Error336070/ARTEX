package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

// drainWithCtx is drain() over a caller-supplied ctx, so a test can cancel the
// parent (pause/kill/shutdown) independently of the run's own MaxDuration budget.
func drainWithCtx(t *testing.T, ctx context.Context, in QueryInput, deps QueryDeps) *Terminal {
	t.Helper()
	var term *Terminal
	for ev, err := range Query(ctx, in, deps) {
		if err != nil {
			t.Fatalf("event err: %v", err)
		}
		if ev.Kind == KindResult {
			term = ev.Terminal
		}
	}
	if term == nil {
		t.Fatal("no terminal event")
	}
	return term
}

// When a tool overruns the MaxDuration wall-clock budget, the tool is interrupted
// at the deadline and the run enters the WRAP-UP phase (settlement) on the live
// ctx — finishing with ReasonTimeout and the wrap-up prompt injected — instead of
// dying with ReasonAbortedTools. This is the hung/slow-tool case that the old
// turn-boundary-only budget check could never settle.
func TestSettlementOnToolOverrunsBudget(t *testing.T) {
	m := &scriptedModel{turns: [][]llm.StreamEvent{
		toolTurn("s1", "sleep", `{"seconds":3600}`), // hangs far past MaxDuration
		textTurn("Facts recorded and summarized"),   // settlement turn → natural stop
	}}
	in := QueryInput{
		Messages:       []llm.Message{llm.UserText("go")},
		Tools:          tool.NewRegistry(tool.NewSleep()),
		PermissionMode: permission.ModeBypass,
		WorkingDir:     t.TempDir(),
		MaxDuration:    100 * time.Millisecond, // budget the run so the sleep overruns it
		Settlement:     &Settlement{Prompt: "SETTLE_ON_TIMEOUT Please record the facts"},
	}
	term := drainWithCtx(t, context.Background(), in, QueryDeps{CallModel: m.call})

	if term.Reason != ReasonTimeout {
		t.Fatalf("reason=%v, want ReasonTimeout (tool wall timeout triggers settlement, not aborted_tools)", term.Reason)
	}
	found := false
	for _, msg := range term.Messages {
		for _, c := range msg.Content {
			if strings.Contains(c.Text, "SETTLE_ON_TIMEOUT") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("Wall-timeout settlement prompt missing from conversation")
	}
	if m.calls != 2 {
		t.Fatalf("Expected 2 model calls (main and settlement); got %d", m.calls)
	}
}

// Regression guard: a PARENT-ctx cancellation (pause / planner kill / shutdown) —
// as opposed to the run's own budget — still aborts with ReasonAbortedTools and
// does NOT enter the wrap-up phase.
func TestParentCancelStillAbortsNoSettlement(t *testing.T) {
	m := &scriptedModel{turns: [][]llm.StreamEvent{
		toolTurn("s1", "sleep", `{"seconds":3600}`),
		textTurn("would-be settlement"), // must NOT be reached
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	in := QueryInput{
		Messages:       []llm.Message{llm.UserText("go")},
		Tools:          tool.NewRegistry(tool.NewSleep()),
		PermissionMode: permission.ModeBypass,
		WorkingDir:     t.TempDir(),
		// no MaxDuration: the only deadline is the parent ctx above
		Settlement: &Settlement{Prompt: "SHOULD_NOT_APPEAR"},
	}
	term := drainWithCtx(t, ctx, in, QueryDeps{CallModel: m.call})

	if term.Reason != ReasonAbortedTools {
		t.Fatalf("reason=%v, want ReasonAbortedTools (parent ctx cancellation aborts without settlement)", term.Reason)
	}
	for _, msg := range term.Messages {
		for _, c := range msg.Content {
			if strings.Contains(c.Text, "SHOULD_NOT_APPEAR") {
				t.Fatalf("Settlement prompt should not be injected after parent ctx cancellation")
			}
		}
	}
	if m.calls != 1 {
		t.Fatalf("Expected no settlement call after parent cancellation; model calls=%d, want 1", m.calls)
	}
}
