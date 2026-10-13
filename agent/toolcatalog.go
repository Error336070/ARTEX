package agent

import (
	"context"
	"encoding/json"

	actool "github.com/Autumn-27/norma/tool"
)

// This file turns built-in tools into an enumerable catalog with database overrides:
//   - BuiltinToolSeeds() expands the three execution agents' tools into seed records (key +
//     description + parameter schema + default agent bindings) for idempotent startup seeding into tools.
//   - ToolResolve reads DB rows at runtime to filter assembled tools by agent,
//     override descriptions/schemas, and inject argument defaults. Keys/handlers stay in code; DB changes prose/defaults only.
// Call handlers always come from code; the DB changes only model-visible descriptions and default arguments.

// ToolSeed is a seedable snapshot: key is CoreTool.Name(), bound permanently to its handler
// and read-only in the UI. Desc/Schema come from code; Agents lists the default bindings.
type ToolSeed struct {
	Key    string         // = CoreTool.Name(), immutable primary key.
	Desc   string         // Top-level description (overridable in UI).
	Schema map[string]any // Parameter JSON Schema (structure read-only; description/default editable in UI).
	Agents []string       // Default agent keys (worker/planner/mainagent).
}

// builtinToolsByAgent constructs each execution agent's domain tools with an empty read-only ToolSet
// (nil stores). Constructors only place closures in Spec without dereferencing stores, making nil safe.
// These tools provide Name()/Description()/InputSchema() only; never Call them here.
//
// SDK general tools actool.DefaultTools() (Read/Write/Edit/MultiEdit/LS/Glob/
// Grep/Bash) are excluded: all agents always have them, with no binding choices. Most descriptions live in Prompt(),
// so overriding only Description() would be incomplete. No seed means no DB row, so ToolResolve
// passes them through unchanged. Only ARTEX domain tools are catalog-managed.
func builtinToolsByAgent() map[string][]actool.CoreTool {
	ts := NewToolSet(nil, "")
	return map[string][]actool.CoreTool{
		"mainagent": ts.MainAgentTools(),
		"planner":   ts.PlannerTools(),
		"worker":    ts.WorkerTools(),
		// The goal decomposer binds set_goals + set_constraints by default to persist extracted goals
		// and operational constraints. It shares managed tools with mainagent; the UI edits descriptions/schemas and bindings.
		"goals": {ts.setGoals(), ts.setConstraints()},
		// Auto binds finding reporting and asset management by default; other domain tools can be enabled in the UI.
		// Seeds initialize new databases; seedAutoDefaultBindings migrates existing databases.
		"auto": {ts.addFinding(), ts.insertAssets(), ts.addCompanyScope(), ts.listAssets(), ts.listCompanies()},
		// Solo pentest binds asset lookup/insertion, finding reporting/lookup, and company lookup by default.
		// Seeds initialize new databases; seedPentestDefaultBindings migrates existing databases.
		"pentest": {ts.listAssets(), ts.insertAssets(), ts.addFinding(), ts.listFindings(), ts.listCompanies()},
	}
}

// defaultUnbound tools remain in the system catalog, visible in the UI and manually bindable,
// but have no default agents. ToolResolve discards unbound tools for every agent; explicit opt-in is required.
// They remain in an agent's base set (for example goal_met in PlannerTools) so seeding can
// read desc/schema and runtime assembly can retain the tool after a user binds it.
//
// goal_met declares the entire task complete without individual prove_goal calls, risking misjudgment and duplicating
// automatic completion after the final prove_goal. It therefore has no default bindings.
var defaultUnbound = map[string]bool{"goal_met": true}

// BuiltinToolSeeds deduplicates all agents' tools into seeds: tools with the same name (such as list_assets
// in multiple agents) merge into one record with unioned Agents; defaultUnbound forces empty bindings.
func BuiltinToolSeeds() []ToolSeed {
	byAgent := builtinToolsByAgent()
	order := []string{"mainagent", "goals", "planner", "worker", "auto", "pentest"}

	type acc struct {
		tool   actool.CoreTool
		agents []string
	}
	m := map[string]*acc{}
	var keys []string
	for _, ak := range order {
		for _, t := range byAgent[ak] {
			a, ok := m[t.Name()]
			if !ok {
				a = &acc{tool: t}
				m[t.Name()] = a
				keys = append(keys, t.Name())
			}
			a.agents = append(a.agents, ak)
		}
	}

	out := make([]ToolSeed, 0, len(keys))
	for _, k := range keys {
		a := m[k]
		agents := a.agents
		if defaultUnbound[k] {
			agents = []string{} // Catalogued and manually bindable, but unbound by default (store [] rather than null, matching other tools).
		}
		out = append(out, ToolSeed{
			Key:    k,
			Desc:   a.tool.Description(),
			Schema: a.tool.InputSchema(),
			Agents: agents,
		})
	}
	return out
}

// ToolResolve, if set, post-processes an agent's fully-assembled tool list against
// the DB tools table: it drops tools not bound to this agent (or globally disabled)
// and wraps the rest so the model sees the DB-overridden description/schema and
// Default arguments get injected. Tools with no matching DB row (MCP/skill/host tools like
// traffic) pass through untouched. nil = tools unchanged. Wired in server/assembly.go.
var ToolResolve func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// DecorateTool wraps t so Description()/InputSchema() report the DB overrides and
// Call() injects scalar parameter defaults (from schema's "default" props) whenever
// the model omitted them. Name/Prompt/permission/scheduler flags delegate to t, so
// the tool's identity and handler are unchanged. Empty desc/schema fall back to t's.
func DecorateTool(t actool.CoreTool, desc string, schema map[string]any) actool.CoreTool {
	if desc == "" {
		desc = t.Description()
	}
	if len(schema) == 0 {
		schema = t.InputSchema()
	}
	return &overriddenTool{CoreTool: t, desc: desc, schema: schema}
}

// overriddenTool is a CoreTool decorator: it embeds the original (so all behavioral
// methods — Prompt/IsReadOnly/IsConcurrencySafe/CheckPermissions/Name — delegate)
// and overrides only the model-facing description/schema plus default injection.
type overriddenTool struct {
	actool.CoreTool
	desc   string
	schema map[string]any
}

func (o *overriddenTool) Description() string         { return o.desc }
func (o *overriddenTool) InputSchema() map[string]any { return o.schema }

func (o *overriddenTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	return o.CoreTool.Call(ctx, injectDefaults(in, o.schema), tc)
}

// injectDefaults fills scalar parameter defaults declared in the (possibly edited)
// schema into the input JSON whenever the model omitted the field or left it empty/
// null. Structure (names/types/required) is untouched — onlydefault values are merged in.
func injectDefaults(in json.RawMessage, schema map[string]any) json.RawMessage {
	defs := scalarDefaults(schema)
	if len(defs) == 0 {
		return in
	}
	m := map[string]json.RawMessage{}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &m); err != nil {
			return in // non-object input: don't touch it
		}
	}
	changed := false
	for k, dv := range defs {
		if cur, ok := m[k]; !ok || isEmptyJSON(cur) {
			m[k] = dv
			changed = true
		}
	}
	if !changed {
		return in
	}
	b, err := json.Marshal(m)
	if err != nil {
		return in
	}
	return b
}

// scalarDefaults extracts properties[k]["default"] for scalar params (string/
// integer/number/boolean). Array/object defaults are skipped: merging them is
// ambiguous and not worth the surprise.
func scalarDefaults(schema map[string]any) map[string]json.RawMessage {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		dv, ok := p["default"]
		if !ok || dv == nil {
			continue
		}
		switch p["type"] {
		case "string", "integer", "number", "boolean":
			if b, err := json.Marshal(dv); err == nil {
				out[name] = b
			}
		}
	}
	return out
}

func isEmptyJSON(raw json.RawMessage) bool {
	s := string(raw)
	return s == "null" || s == `""`
}
