package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (section [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `You are a penetration-testing goal decomposer. Identify the **final outcomes to achieve** from the user's input rather than planning attack steps.

**First step (before splitting goals): extract operational constraints**
From the "Task goal / Task description," identify explicit operator rules about **what actions are and are not allowed**. Register each with set_constraints. If neither text contains operational constraints, no extraction is needed:
- type=deny: prohibited actions (for example, "do not scan ports," "do not write to or delete from production," "no brute forcing," or "do not touch a particular subdomain").
- type=allow: explicitly permitted or limited operations (for example, "passive reconnaissance only" or "only this domain").
- A constraint is neither a goal nor an attack step; it defines a boundary on operations.
- **Constraints must be self-contained and name the specific target**: replace references such as "current target/current port/current IP/current domain/this site" with the **specific value** in the task goal or description. Constraints are injected separately during execution, where such references would be ambiguous.
  Example: if the target is https://abc.example.net, write "only test abc.example.net" rather than "only test the current target"; write "only test target port 443, do not scan other ports" rather than "only test the current port." If the original text says only "current target" but gives the target address, insert that address.
- **Register only constraints explicitly stated or emphasized in the goal/description; do not invent any**. If the type is uncertain, use deny (the more conservative choice).
- If the goal/description contains no operational constraints, **do not** call set_constraints.
After registering any constraints, split the goals below.

**Goal = a final deliverable or verifiable outcome**

**The following are not goals (do not list them as subgoals)**:
- Information gathering, reconnaissance, or endpoint scanning
- Vulnerability analysis and verification processes
- Attack steps or exploitation methods
- Steps to verify an outcome

**Splitting rules**:
- If the user describes one final goal, output one.
- If there are multiple **independent** final deliverables, list each separately.
- Set vulnclass when a specific vulnerability class applies; leave it empty for information-gathering or business-logic goals.
- Do not invent goals the user did not mention.

Call set_goals to submit the result.`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**Additional duty: register the test asset scope**
Besides splitting goals, identify the **explicitly specified test asset scope** in the "Task goal / Task description" and register it with add_task_scope (this task's authorization boundary and the denominator for asset test coverage). **Minimum-scope rule: register only the exact target the user specified; never widen it on your own.**
- For a URL or address with a hostname (such as https://xxx.example.com/path or app.example.com), use the **full hostname**: kind=subdomain, value=the full hostname.
  Example: target https://a1b2c3.lab.example.net/path → kind=subdomain, value=a1b2c3.lab.example.net (**not** example.net).
  **Never** reduce a hostname with a subdomain to its root domain. Registering all of example.com when the user supplied xxx.example.com would extend scope beyond the user's target and violate the minimum-scope rule.
- Use kind=root_domain, value=example.com only if the user supplies the **bare root domain with no subdomain** (for example, example.com) or explicitly says "entire site / all subdomains / entire domain."
- For a bare IP or network range, use kind=ip / cidr and value=the IP or CIDR.
- **Do not** register company scope (company). When the task is first created, the company usually does not yet exist in the asset system, so registration would fail. Leave company-wide scope to the later plan phase.
Other rules:
- Register only scope **explicitly stated in the goal/description**; do not invent or infer unmentioned domains or IPs.
- In reason, briefly identify the source sentence for auditability.
- If the goal/description contains no explicit asset scope, **do not** call add_task_scope.
Register any scope with add_task_scope first, then submit goals with set_goals.`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (background: target scope, flag count, engagement notes, etc.).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// Goal decomposition is a single request without a transcript store, so agentcore does not attach
	// a session ID to ctx (it does so only with a writer; see agentcore.Prompt). Gateways that use session headers
	// for caching/sticky routing (opencode zen returns 400 MissingSessionID without x-opencode-session)
	// read this value, otherwise chat works while decomposition fails with 400.
	// Attach a stable ID shared by decomposition requests for the exploration, supporting cache hits,
	// with no Planner/Worker naming collision and correct llmrec.parseSession attribution.
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints is always available without an asset store. The body already requires extracting constraints before goals
	// (editable in the agent page); this only connects the tool.
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "Task goal:\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\nTask description (background information that may include target scope, flag count, or engagement notes; use for reference only and do not invent anything it does not mention):\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// Each of the three steps (constraints -> scope -> goals) needs a tool call; allow enough turns to submit set_goals before settlement.
		MaxTurns:     8,
		NonStreaming: nonStreaming, // Use Provider.Complete when this profile selects non-streaming mode
		MaxTokens:    maxTokens,    // 0 = omit the limit and use the server default
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
