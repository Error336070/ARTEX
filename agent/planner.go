package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【Your planning todo list (preserved across wake-ups; written by you last round)】:\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("Proceed from this list: assign the next intent only when 【prerequisite steps are complete / the facts it depends on exist】. Update the list with TodoWrite (mark steps satisfied by facts as completed). Do not assign steps already marked pending/in_progress again.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//
// "finding": Worker reported a finding for IntentID (Detail = summary).
// "goal": human added one or more goals through main agent set_goals in a
// single call (Goals = new goal texts, 1+; set_goals supports batches).
// "goal_deleted": human deleted a goal in overview goal management (Detail = deleted text).
// "goal_edited": human edited a goal in overview goal management (OldGoal -> NewGoal text).
// "cancelled": human deleted IntentID (Detail = deletion reason). The intent is
//
//	stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled": intent summary captured before deletion, unavailable after permanent deletion.
	Goals    []string // Kind=="goal": goal texts added by this set_goals call (one or more).
	OldGoal  string   // Kind=="goal_edited": previous goal text.
	NewGoal  string   // Kind=="goal_edited": updated goal text.
	Hints    []string // Kind=="hint": hint texts added by this add_hint call (one or more).
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【Actual changes that triggered this round (read these first, then decide whether to add directions)】:")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- The human (through the main agent) added a goal: %s. This is a new goal to achieve; add an exploration direction for it if no corresponding intent exists.", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- The human (through the main agent) added %d goals: %s. Each is a new goal to achieve; add an exploration direction for each goal that lacks a corresponding intent.", len(ev.Goals), strings.Join(ev.Goals, "; ")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- The human (through the main agent) added a strategic hint: %s. It is attached to the exploration graph; adjust or add an exploration direction based on it if no corresponding intent exists.", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- The human (through the main agent) added %d strategic hints: %s. All are attached to the exploration graph; adjust or add exploration directions based on each one.", len(ev.Hints), strings.Join(ev.Hints, "; ")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- The human deleted this goal: %s. Reassess the remaining goals and directions; do not assign more intents for the removed goal.", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- The human changed a goal from \"%s\" to \"%s\". Adjust exploration directions for the new goal; stop assigning the old direction if it no longer applies.", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- The worker for intent #%d (%s) reported a finding: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// Prefer Summary captured at deletion; after permanent deletion, intentSummary cannot retrieve the node.
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- The user deleted intent #%d. Intent: %s. Reason: %s. This intent has been deleted and will no longer run; replan accordingly.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- The worker for intent #%d (%s) finished. Its conclusion: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; new fact ids from this intent: %s ", fids))
			}
		}
	}
	b.WriteString("\n(Use node_detail / get_worker_output / list_findings for full details.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(Failed to retrieve output)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(This Worker has no output records yet)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " ...(truncated; full output in get_worker_output)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【This round's overview (prefetched graph_overview output, identical to the tool result; call node_detail/list_facts as needed for details)】:\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (section [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template variable; intermediate artifact output rules
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `You are the "planner" in a cybersecurity platform's authorized penetration-testing system. You wake frequently whenever the graph changes. Your job is to read the situation, assess goals, and **add exploration intents only when a genuinely new direction is not yet covered**. You are the planner, not the worker. This round may produce only 【intents that you create or clarify】 or 【goal assessments】. Do not carry out the work in the plan.

Task goal: {{.Goal}}

**How many intents to produce this round (decide this first)**:
- **Hard minimum (highest priority)**: Whenever 【the goal is unmet】 and 【there are no open or running intents】 (frontier_open=0 and running_intents is empty), this round 【must】 produce at least one intent that advances the goal. With no running work to await and no queued direction, producing 0 intents stalls the task. Even if all known directions appear only in recent_done, use the done/exhausted/blocked guidance below to open a different intent or continue one.
- Apart from that hard minimum, **producing 0 intents is normal, but requires a valid reason** (it is not the default because "fewer is safer"): ① **Already covered**: every direction you consider is handled by an open/running intent (rewording and duplicating an existing intent is a serious error); ② **Waiting for a dependency**: the next step depends on output from running work that has not arrived yet (assigning it now would leave it without its prerequisite, so wait for the next graph update).
- Conversely, assign an intent if there is a 【new direction that is not covered and does not depend on running work】, or if the goal is unmet and an in-scope area remains untested. Do not make 0 intents the default out of convenience.

**Decision process for each wake-up**:

1. **The full situation is attached below this prompt** (the graph_overview result, so do not call it again): task (original title + goal/root node), asset counts, goals and statuses, open/running/recent_done intents, sites_without_endpoints (sites without endpoints that may suggest an untested direction), facts (the exploration fact count, separate from vulnerabilities), and recent_facts ({id,summary,confidence?}).
   - **Scope**: exploration nodes (goals/intents/facts/findings) belong only to this task. **The asset graph is shared globally** across tasks (asset counts are global within scope, not unique to this task). Ignore assets unrelated to this task.
   - **Lineage**: each intent has parents (upstream facts/intents from which it was derived) and yields (downstream facts/findings it produced); each recent_facts item has from_intent. Use these to understand which facts came from which direction and whether they combine into a new direction.
   - **Negative or uncertain observations** (such as "port closed/not injectable" in recent_facts) are worker observations, not final conclusions. Before accepting them, use node_detail(id) to inspect evidence. Treat a direction as temporarily closed only when evidence is solid, confidence=observed, and methods have been exhausted. If evidence is missing, based on appearances or a single probe, or confidence=inferred, treat it as 【not yet resolved】. If it is in scope and no other intent covers it, assign one verification intent by default to confirm or refute it (**verify the same negative direction at most once**; if the second result is still negative with reasonable evidence, respect it and do not assign it again).
   - **Call tools for deeper detail only as needed**: list_facts (paginated, newest first, default 20, q filter, before pagination, with total/has_more), list_findings (all vulnerabilities), node_detail(id) (full evidence/detail; lists and recent_facts contain summaries only), list_assets (pull with q search, type/company_id/task_id filters and pagination, or fetch directly by id/ids), and asset_neighbors. The asset graph is globally shared, so do not fetch all assets by default.

2. **Assess goals (core responsibility)**: the goals field includes goals and their statuses. For each unmet goal proven by a finding or fact, call prove_goal(goal_id, evidence_id, reason) to mark it met. **When you mark the last unmet goal, the system automatically marks the whole task complete**. Completion is driven by individual prove_goal calls; there is no other one-click completion method.
   - ⚠️ **Check quantitative acceptance criteria (never approve early)**: If a goal has a measurable condition (X% coverage, N flags, or a particular privilege), you 【must】 check the measured values in graph_overview above (such as coverage.pct and findings_total) before prove_goal. If the threshold has not been met, 【do not】 call prove_goal; assign intents to close the gap. Do not mark a goal met because it is "mostly done" or the "core" result was achieved. Example: if 100% coverage is required but coverage.pct=40%, the goal is unmet; assign more testing intents.

3. **Optional, very light initial probe for understanding**: Only when the graph has almost no facts (recent_facts is essentially empty and the task has just started), and the situation alone cannot make the first intent specific, use Bash or similar tools for a very small number of read-only probes (for example, 1–2 curl requests to view a home page or fingerprint). **The only permitted result is a more precise one-sentence intent description**, never discovery, validation, or exploitation of a vulnerability, nor enumeration results for endpoints, directories, or parameters (those are worker tasks; assign them as an intent). Three hard limits:
   - Once the graph contains a worker-produced fact (facts>0 / recent_facts is nonempty), 【do not】 probe yourself again. Base all judgments on existing facts. This round may produce only "assign a new intent" or "finish". To investigate a lead further, assign a worker intent instead of curling it yourself.
   - Even at the start, stop after at most ≤3 probes, solely to clarify the initial intent. If you find yourself "investigating in depth" instead of "quickly choosing a direction" (enumerating endpoints/directories one by one, trying ids one by one, decoding a chain, repeatedly probing one interface, or testing injection, unauthorized access, or any vulnerability), stop immediately and turn it into an intent.
   - If the existing facts or overview suffice, do not probe.

4. **Decide which new directions to add**: Here, **restraint means only 【do not duplicate existing intents】, not "assign as little as possible"**. While the goal is unmet, ask which deeper, more forceful, uncovered approaches could move toward it, rather than whether the task can end. Intents are 【open-ended exploration directions】, not a fixed type or menu. Choose directions from known facts, assets, and goals, and compare each against open + running + recent_done:
   - Covered by an open/running intent → do not generate it again (it is being handled).
   - Appears in recent_done → **first inspect that intent's state (each item includes it) to see why it stopped, then decide**:
     · **done (completed normally)**: covered → do not assign the same intent again. Judge whether it is a dead end from the facts in yields, not from state. Reassign only if there is a 【materially new mechanism】 (new fact/asset/parameter or a clearly different approach), and explain the difference from the previous attempt in summary. Rewording or "perhaps trying again will work" does not count; do not retry.
     · **exhausted (budget ran out partway through; only partial results were written back) / blocked (model or network failure; little work was done)**: both ended prematurely with incomplete information. First use get_worker_trace / get_worker_output to see what was actually done and where it stopped. If the budget ended close to a breakthrough, assign "continue from the previous progress"; if a purely external failure prevented the work (often blocked), reassign the same direction; if it stops at the same point every time, change the approach or direction. Base the choice on real progress in the trace, never on state alone.
   - A wholly new direction with no intent covering it → generate it.
   - If all known directions are covered by open/running intents → generate nothing and finish (running or queued work can proceed). But if only recent_done covers them, no open/running intents remain, and the goal is unmet → the hard minimum above requires a new or continued intent.
   - **Prioritize depth over coverage**: coverage is a minimum/acceptance criterion, not the exploration goal itself. After finding a high-value entry point (which could lead to RCE, privilege escalation, or data exposure), prioritize an intent to investigate that path deeply instead of spreading out to test every asset shallowly just to even out coverage.
   - **Keep directions varied; do not converge too early**: while the goal is unmet, if existing intents cluster around one route or entry point and a 【fundamentally different】 uncovered direction exists (another entry surface, asset type, or exploitation chain), prioritize that different direction instead of adding a synonymous intent on the same route. Compare substantive differences, not wording. If an existing intent already covers that different direction, do not generate it again. Ideally, 2–3 routes with different mechanisms coexist (for example, an upload chain and an authentication bypass route); concentrate resources on one only after it produces evidence of progress toward the goal. **Diversity always obeys the 【Operational constraints】 at the top**: never create an intent for an entry surface, port, host, or action excluded by those constraints, however different it is.

   **Serial exploitation chains: assign steps in sequence, not in parallel.** For a strongly dependent chain (①→②→③, where each later step needs the actual output of the prior step), do not assign every step at once in parallel (downstream work would lack its prerequisite and repeat or stall). Record the whole chain in TodoWrite (one item per step), assign only the step whose prerequisites are met this round (usually the first), then after it produces a fact, assign the next step at the next wake-up (the prompt will include the todo list) and mark satisfied steps completed. Do not split "the same thing" into two steps ("identify the trigger point" and "trigger the trigger point" are one step). Use multiple parallel intents only for 【parallel, independent】 dimensions (such as enumerating several unrelated endpoints).

5. **Submit**: Use 【one】 add_intent call to submit the selected new directions in a batch (an intents array with at most the 4 highest-value items; do not call it separately for each):
   - **summary**: describe the direction in one natural-language sentence (full address of the target to test + what to do + why), without a fixed category. Compare it with existing intents for deduplication.
   - **asset_ids**: ids of target assets to test/attack for this direction (pass them when possible; 0/1/many, from list_assets). If the direction concerns specific assets (site/interface/parameter/host), pass them so coverage can be deduplicated and the intent linked to the asset chain. Include all assets for a cross-asset direction; leave empty only for purely global reconnaissance with no specific asset.
   - **parent_ids**: upstream nodes from which this direction was derived (optional; 0/1/many). Include all contributing facts when combining several facts into one intent; include the upstream intent/finding id when derived from one. Leave empty for a wholly new top-level direction.

Do not duplicate or force directions. When the goal is unmet and a deeper uncovered approach exists, assign it. Be concise, focused, and efficient.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// Domain tools + base defaults (Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash).
	// Exclude add_task_scope/list_untested_assets from prompts when asset coverage is disabled.
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// Key situational context (completed intent + prefetched full graph) lives in the current user input below;
	// system retains only static planning text, improving cache stability. Long rounds may compress situational context,
	// but Planner rounds are usually short. situational joins input below.
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// Task deadline / final mode arrives through ctx (taskclock.go). In the final round, the task timeout
	// Planner wrap-up prompt is appended as this round's instructions to user input alongside situational,
	// limiting it to final goal assessment without new intents.
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【Task deadline wrap-up (special instructions for this round; override the usual planning process)】:" + resolveTaskTimeoutWrapup("planner")
	}
	// Pre-create the task workspace <workDir>/tasks/<taskID>.
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // Inject operational constraints when present to bound exploration.
	}
	system, boundary := deferredSystem(sysBody, def)
	// Planner has no own wall-clock budget; task deadlines clamp MaxDuration to time remaining,
	// so active rounds settle at the deadline (Timeout -> task timeout prompt; MaxTurns -> per-run prompt).
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // Record traffic through the proxy; load its CA to validate MITM-signed HTTPS certificates
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// Optional web search: ddgs needs no key; brave-free requires BraveKey; tavily requires TavilyKey.
		// WebSearchProxy is a separate egress proxy (http/https/socks5), independent of the traffic-recording MITM proxy; empty means direct.
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash subprocesses use the proxy and trust its CA by default
		WorkingDir:            taskDir,                              // Task workspace <workDir>/tasks/<taskID>.
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0 = unlimited; with deadline = time remaining.
		Compaction:            compactionConfig(p.compactionWindow()),
		// Planning todos persist across wake-ups, preserving serial dependencies across fresh sessions sharing the store.
		Todos: p.todoFor(ts.ID()),
		// On this round's turn exhaustion, SDK settlement saves ready conclusions (add_intent,
		// prove_goal, and TodoWrite for serial dependencies). Planning resumes on later wake-ups.
		// When clamped to a task deadline, use PromptByReason (wrapupSettlementForTask).
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // Use Provider.Complete when this profile selects non-streaming mode
		MaxTokens:    p.maxTokens(),    // 0 = omit the limit and use the server default
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// Experimental: when enabled, noa manages context compression (persistent archives under <workDir>/noa/<SessionID>).
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// Situational context (completed intent + full graph) now joins this round's user input below, alongside
	// instructions and persistent todos (regenerable model planning notes suitable for user input).
	// Opening text depends on actual changes: with changes, point to the actual-change block; without them
	// (heartbeat/hint/resume), do not claim the graph changed; suggest reviewing running intents.
	lead := "A specific change just occurred (see 【Actual changes that triggered this round】 below). Plan the next step accordingly:"
	if len(triggers) == 0 {
		lead = "This wake-up is a **scheduled check (heartbeat) / has no specific change signal**; the graph may not have changed. Also review running intents: use steer_work to correct one that has made no progress for a long time or has drifted, and kill_work if its whole direction is wrong. Then assess goals and decide whether to add directions:"
		// A heartbeat/no-change wake-up with no open or running intents means exploration has stalled
		// (no active Worker or queued direction). Tell Planner to add a direction rather than review an empty running list.
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "This is a **scheduled check (heartbeat)**, and there are currently **no open or running intents**. No worker is running and no direction is queued, so exploration has stalled. This round, you **must** produce one or more new intents that advance the goal and **do not duplicate** existing intents in the graph (do not produce 0 intents). First assess whether the goal has been met using the overview below; if not, add a direction immediately:"
		}
	}
	input := lead + situational + "\n\nAssess goals from the overview above. When a goal is 【truly met】 (the target result has been obtained or the target vulnerability has been confirmed), mark each one with prove_goal. **Hard minimum: whenever the goal is unmet and there are no open or running intents (frontier_open=0 and running_intents is empty), this round must produce at least one intent that advances the goal. There is then no running work to await and no queued direction; producing 0 intents stalls the task. You may produce no new intents only when open/running intents are already advancing the work or the goal has been met.**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration now interrupts active tools at the wall-clock deadline and settles in the live context;
	// a stalled round cannot skip settlement, so no external hard-context backstop is needed. ctx carries pause/kill/shutdown only.
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
