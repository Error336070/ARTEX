package server

import "testing"

func TestLogSeverityPreservesTranslatedAndHistoricalMessages(t *testing.T) {
	for _, tc := range []struct {
		message string
		level   string
	}{
		{"[mcp] connection failed: timeout", "error"},
		{"[mcp] 连接失败: timeout", "error"},
		{"[activity] discarded activity record", "error"},
		{"[activity] 丢弃活动记录", "error"},
		{"[seed] refused target", "error"},
		{"[seed] 拒绝目标", "error"},
		{"[mcp] service unreachable", "error"},
		{"[mcp] 服务不可达", "error"},
		{"[worker] retry after 1s", "warn"},
		{"[worker] 1s 后重试", "warn"},
		{"[tools] disabled", "warn"},
		{"[tools] 禁用", "warn"},
		{"[llmpool] failover enabled", "info"},
		{"[worker] failed; retry after 1s", "error"},
	} {
		if got := levelOf(tc.message); got != tc.level {
			t.Errorf("levelOf(%q) = %q, want %q", tc.message, got, tc.level)
		}
	}
}
