package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "The user paused the task",
		"The user paused the task through the task control API (POST /api/tasks/{id}/control, action=pause). This Planner/Worker run was cancelled; running intents return to frontier(open), are claimed again after resume, and restart from the beginning")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "Orchestrator paused the task",
		"The orchestration agent called pause_task to pause this task. This Planner/Worker run was cancelled; running intents return to frontier(open) and restart after resume")
	AbortTaskDeleted = cause("task_deleted", "Task deleted",
		"The task is being deleted (DELETE /api/tasks/{id}). The deletion barrier cancelled its running Planner, Workers, and main agent; results from this run will no longer be used")
	AbortPausedOnReload = cause("paused_on_reload", "Paused state restored on reload",
		"At startup, the backend restored the paused task state from the database. This run was cancelled; normally no agents are running during recovery")
	AbortGoalMet = cause("goal_met", "Planner determined goals were met",
		"The Planner determined that the task goals were met and set the task to done, then cancelled the remaining Workers. These intents are marked stopped rather than failed")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "Task wrap-up wait expired",
		"After the task timed out, it waited for running Workers to wrap up gracefully. The 90-second drain grace period was insufficient, so they were forcibly cancelled. Intents are marked exhausted; facts and assets already saved during wrap-up are retained")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "Planner stopped this intent",
		"The Planner called kill_work to stop this intent, usually because it had drifted or was no longer useful. The intent is marked stopped and will not be claimed again automatically")
	AbortWorkPausedByUser = cause("work_paused_by_user", "The user paused this Worker intent",
		"The user paused the running Worker. This call was cancelled and the intent becomes paused. Registered intents, facts, findings, and activity records are retained; execution restarts from the beginning after resume")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "User deleted this Worker intent",
		"The user deleted the running Worker. This call was cancelled. After the Worker leaves the write section, the server applies the selected deletion mode: soft deletion marks the intent deleted and retains all outputs; permanent deletion removes it and downstream nodes supported only by it")
	AbortWorkFinished = cause("work_finished", "Worker finished; context released",
		"The Worker finished normally and the engine released its context in detachWork. This is not an interruption; if shown in an interruption message, cancellation raced with finalization")
	AbortPausedRaceGuard = cause("paused_race_guard", "New run blocked while task paused",
		"While the task is paused, the engine refuses to issue new execution contexts to prevent a claim/pause race from starting a Worker. Claimed intents return to frontier")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "The user stopped this turn",
		"The user clicked Stop to cancel this main agent or conversation agent turn. Activity records are retained, and another message can be sent")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "Task pause cancelled main agent chat",
		"When the user paused the task, the running main agent conversation was also cancelled. Activity records are retained; this message is not replayed automatically after resume")
	AbortChatTurnFinished = cause("chat_turn_finished", "Turn finished; context released",
		"This turn finished normally and the server is releasing its context. This is not an interruption; if shown in an interruption message, cancellation raced with finalization")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "Backend is shutting down",
		"The backend received SIGINT or SIGTERM and is restarting, updating, or shutting down. All running agents are cancelled; remaining running intents are reset to open and executed again after restart")
	AbortRunHardTimeout = cause("run_hard_timeout", "Run hard timeout triggered",
		"The run exceeded its soft wall-clock budget and extra grace period. A model request or tool has not returned for a long time, preventing normal wrap-up at the turn boundary. Check the last tool call that had not returned before interruption")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "The upstream context reached its deadline",
			"The upstream context reached its deadline, but its creator did not attach a named cause using WithTimeoutCause: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "The cancelling caller did not attach a named cause",
			"The upstream context was cancelled without a named cause through context.WithCancelCause. Register the cause in agent/cancelcause.go and attach it at the cancellation site", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
