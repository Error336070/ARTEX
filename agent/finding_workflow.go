package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**Finding ID contract**: finding_id is the independent finding record ID; finding_node_id is the exploration node ID. The id returned by list_findings / list_task_findings / node_detail / get_task_node_detail remains an exploration node ID; read the independent ID from finding_id in the same response. get_finding_traffic / bind_finding_traffic use the independent finding_id. The legacy update_finding_report finding_id parameter still takes finding_node_id. Do not use the number in report_finding's first line for evidence tools or guess other numbers after an ID error."

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\nBy default, the report agent verifies and binds traffic before writing the report. Preserve verification commands, key output, existing real traffic IDs, and their purpose in evidence so it can compare them with execution records; no extra packet lookup is needed just for binding. Explicit immediate binding remains supported: traffic_refs or evidence_hint_id submits verified references, with the latter reading structured references from the specified task hint; any invalid reference fails the entire submission. TCP/no-packet cases do not require these optional parameters. Returned finding_id and finding_node_id identify the independent record and exploration node respectively."
		case "add_hint", "add_task_hint":
			note = "\nWhen handing off a confirmed finding, preserve verified traffic IDs, roles, notes, and order in that hint's traffic_refs (top-level for one hint, within the corresponding hints item for batches). Describe the specific finding it supports in text. Do not hand off only text while discarding existing traffic references. Unverified candidates must not be passed as evidence."
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**Traffic evidence handoff (optional)**: by default, the report agent performs automatic binding after the finding is saved and before writing its report. Preserve verification commands, key output, existing real traffic IDs, and their purpose in evidence, plus intent_id within tasks for traceability. No extra packet lookup is needed just for binding. Auto / Planner must retain the executor's existing references when reporting on its behalf. add_hint / add_task_hint can hand off traffic_refs; report_finding still supports explicit immediate traffic_refs / evidence_hint_id binding. Register TCP or missing-packet cases normally; never guess IDs or repeat probes merely to obtain packets."
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\nWithout task context in a platform conversation, do not call report_finding directly. Hand off to the existing relevant task with add_task_hint for its agent to register, and verify the result using list_task_findings."
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\nComplete submission/handoff of existing evidence before assessing goal completion. Do not end the task and cancel Workers solely because a text finding was saved while evidence handoff is still pending. Missing packets do not require waiting or forced capture."
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**Automatic traffic association before reporting (enabled)**: verify and bind traffic for this finding, then write its report. Obtain explicit finding_id and finding_node_id from report_finding JSON or get_task_node_detail / list_task_findings. Read finding details, the corresponding intent's execution records, and existing evidence, preferring real IDs supplied in the handoff. For HTTP verification with available traffic tools, use traffic_search to select candidates and traffic_get to verify each request/response supports the finding; domain and time only filter candidates and do not prove ownership. Bind confirmed evidence in reproduction order with bind_finding_traffic(finding_id, traffic_refs), choosing baseline / proof / verification / supporting with purpose notes. Operate only on this finding; do not duplicate it or probe the target again. After successful binding, reread get_finding_traffic for the latest version and required bodies; pass the version actually read as evidence_version to update_finding_report (whose finding_id parameter still takes finding_node_id). Do not append existing bindings again. For TCP, uncaptured traffic, unavailable tools, or no exact match, skip automatic binding and write the report from text/command evidence with the reason. Never guess references to fill gaps. Do not claim success after binding failure; retain existing evidence and explain the missing binding in the report."
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "Optional verified traffic references for the specific finding in this hint, preserving order; report_finding can later carry them through evidence_hint_id.", "items": obj(map[string]any{"traffic_id": str("Real traffic ID"), "role": str("baseline / proof / verification / supporting"), "note": str("Which conclusion this traffic supports")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d must be a hint node in this task (inherited hints cannot be bound directly)", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
