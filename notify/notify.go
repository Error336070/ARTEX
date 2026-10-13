// Package notify implements messaging/email adapters for findings. This leaf package depends only
// on the standard library, not db/server. Configurations arrive as map[string]any
// (notification_channels.config JSONB), content as Message. Signing, UTF-8 truncation, and filters
// can be tested without PostgreSQL; server owns orchestration. Channel implementations must be
// stateless and reused concurrently across configurations/bots. All credentials come from cfg;
// never cache Webhook URLs or similar settings in implementation fields.
//
//
//
//
package notify

// Channel identifiers are valid notification_channels.kind values, allowlisted in server rather
// than DB CHECK, as with findings.status, to simplify future additions.
const (
	KindDingTalk = "dingtalk" // DingTalk custom bot.
	KindFeishu   = "feishu"   // Feishu/Lark custom bot.
	KindWeCom    = "wecom"    // WeCom group bot.
	KindWebhook  = "webhook"  // Generic Webhook: custom method, headers, and JSON template.
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP email.
)

// Event types corresponding to notification_events.kind.
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind is the fallback for an empty configuration kind.
const InitKind = KindDingTalk

// severityRank maps severity to comparable ordinals. Unknown values return 0 and are rejected by
// any min_severity threshold to avoid uncertain notification noise.
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank returns severity ordinals, or 0 for unknown values.
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel returns an emoji and severity label for titles and card colours. Unknown values
// are returned unchanged rather than invented.
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 Critical"
	case "high":
		return "🟠 High"
	case "medium":
		return "🟡 Medium"
	case "low":
		return "🔵 Low"
	default:
		return severity
	}
}

// StatusLabel returns readable disposition labels for status-change messages.
func StatusLabel(status string) string {
	switch status {
	case "pending":
		return "Pending"
	case "in_progress":
		return "In progress"
	case "confirmed":
		return "Confirmed"
	case "resolved":
		return "Handled"
	case "fixed":
		return "Fixed"
	case "false_positive":
		return "False positive"
	case "ignored":
		return "Ignored"
	case "duplicate":
		return "Duplicate"
	case "risk_accepted":
		return "Risk accepted"
	default:
		return status
	}
}

// AtLeast checks severity against min. Empty min always passes; unknown severity ranks 0 and fails
// every nonempty threshold (severityRank).
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}
