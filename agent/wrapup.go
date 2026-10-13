package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// Wrap-up / settlement prompts are injected by the SDK settlement phase when an agent exhausts
// its turn budget (MaxTurns) or times out (run_seconds/MaxDuration),
// so it saves identified results before producing a summary, avoiding lost work.
//
// Each agent's wrap-up prompt can be overridden in agents.wrapup_prompt; empty uses the
// built-in default. Only the prompt body is editable; disabled tools and wrap-up turn budgets are code policy.

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// Built-in wrap-up prompts indexed by agent key. Worker reuses the historical settleWrapUpPrompt
// (in worker.go); Planner and main agent have separate prompts; custom agents use the generic fallback.
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults gives each agent's own wrap-up turn budget (positive backend values override it).
// All receive 10 turns to allow persistence; missing keys use genericWrapupTurns.
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "Your planning step budget for this round is nearly exhausted. Only this round is ending; the system may wake you again as circumstances change, so the task is not ending and you need not close out the whole plan now. Persist the conclusions you have already reached, but do not manufacture intents merely to finish this round (zero intents is a normal result): (1) If you have identified exploration directions that should be dispatched now, submit them together with one add_intent call; (2) for goals proven achieved by a finding or fact, call prove_goal to mark them met; (3) if you identified a multi-step sequential exploitation chain, record it with TodoWrite so planning can continue on the next wake-up. Then end this round directly, without summary text."

const mainAgentWrapUpDefault = "Your turn budget is nearly exhausted and this interaction is ending. Do not initiate further exploration or operations. In **one separate plain-text sentence**, summarize current progress, key conclusions, and the suggested next step for the user."

const genericWrapUpDefault = "You will be stopped because the budget is exhausted. First save completed results that have not yet been persisted, then summarize what you did and the key conclusions in **one separate plain-text sentence** (shown as the result of this run)."

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- Task timeout wrap-up prompts (see docs/任务级超时与收尾设计.md) ----------
//
// Task timeout prompts differ from per-run prompts: the latter mean this run exhausted its budget;
// the former mean the entire task is ending. The meanings may be opposite (Planner per-run prompts
// continue planning, while task timeout prompts stop planning and make a final assessment). Worker/Planner only.

// WrapupTaskTimeoutOverride / ...TurnsOverride provide database overrides for timeout prompts and turns
// (wired to agents.task_timeout_wrapup_prompt / _max_turns for Worker/Planner only).
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**The entire task has reached its timeout and is about to end** (this is not the budget for this run; the whole exploration is ending). This is the last chance to: (1) persist **all** identified results that have not yet been saved—new assets with insert_assets, exploration conclusions/facts with record_fact, and confirmed vulnerabilities with report_finding; (2) start no new commands or probes; (3) **finish with one separate plain-text sentence** summarizing your key conclusion for this intent."

const plannerTaskTimeoutDefault = "**The entire task has reached its timeout and is about to end** (the whole task, not just this round). Make one final goal assessment based on **all** current facts and findings: for goals proven achieved by evidence, call prove_goal to mark them met. **Do not generate any new intents** (they would not be executed now). End after this assessment, without summary text."

// TaskTimeoutWrapupDefault returns the built-in task timeout prompt for placeholders/restoring defaults.
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // Empty for unconfigured agents (mainagent/chat).
}

// resolveTaskTimeoutWrapup: non-empty DB override > built-in default. Empty means no task timeout prompt
// (non-Worker/Planner agents); callers should use the per-run prompt.
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // Use the per-run turn budget by default.
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true: the task deadline bounds this run. Timeout settlement uses the task timeout prompt;
//     MaxTurns settlement uses the per-run prompt when turns run out before the task deadline.
//   - clamped=false: the task deadline is distant; both reasons use the per-run prompt (wrapupSettlement).
//
// The harness selects PromptByReason using the actual settlement reason, avoiding mismatches at build time.
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // Fallback (also used for both reasons when not clamped).
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // Task deadline reached.
				harness.ReasonMaxTurns: perRun, // Turns exhausted while task time remains.
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
