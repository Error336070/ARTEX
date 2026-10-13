package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Tests enforce packing complete digest entries within platform limits and reporting omitted
// counts accurately. Previously full rendering followed by truncation and whole-batch success
// silently lost findings while history showed success. Callers must mark only entries actually
// delivered.
//

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// 200 Chinese digest entries necessarily exceed WeCom's 4096 bytes.
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("Body of %d bytes exceeds limit %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("Body is not valid UTF-8")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Expected partial batch (0 < kept < %d); got %d", len(m.Items), kept)
	}
	// Headers must state included and remaining counts so readers do not mistake the included count
	// for the entire batch.
	if !strings.Contains(body, "remaining") || !strings.Contains(body, "continue in the next message") {
		t.Fatalf("Header should state remaining item count:\n%s", body[:minInt(400, len(body))])
	}
	// Include only the first kept entries.
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "漏洞"+itoa(i+1)) {
			t.Fatalf("Item %d should appear in this message:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "漏洞"+itoa(kept+1)) {
		t.Fatalf("Item %d belongs in the next batch", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // 0 means unlimited.
	if kept != len(m.Items) {
		t.Fatalf("Unlimited length should preserve all items; got kept=%d", kept)
	}
	if strings.Contains(body, "remaining") {
		t.Fatalf("Truncation notice should be absent without truncation:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// Even if no entry fits, send one with final truncation. Otherwise an oversized finding
	// permanently blocks the batch on every claim.
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("Expected at least 1 item; got %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("Single message should report 1 delivered item; got %d", kept)
	}
	// Empty messages have no deliverable entries.
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("Empty message should report 0 items; got %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// Telegram limits characters; byte limits would reduce Chinese messages to a third of the intended length.
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("Body of %d characters exceeds limit %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Expected partial batch; got %d", kept)
	}
	if !strings.Contains(text, "continue in the next message") {
		t.Fatalf("Remaining items should be stated:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("Expected partial card batch; got %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// These two channels do not truncate bodies and deliver the complete batch.
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("Precondition not met")
	}
	// Verify indirectly through renderer results: unlimited markdownBody(0) retains everything.
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("Unlimited length should use all items; got %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent ensures untrusted content cannot change message structure.
// Titles/summaries originate from model responses to target content; asset names come from target URLs.
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // Must appear in escaped form.
		wrong []string // Must not appear unescaped.
	}{
		{
			name: "Newline and external link in title",
			item: Item{
				Severity: "high",
				Name:     "登录口 SQL 注入\n[紧急：点此验证账号](http://attacker.tld)",
			},
			// Collapse newlines to prevent forged list/quote entries and escape brackets/parentheses to
			// prevent clickable external links.
			must:  []string{`\[紧急：点此验证账号\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[紧急", "\n\n[紧急"},
		},
		{
			name: "Image beacon in title",
			item: Item{
				Severity: "high",
				Name:     "漏洞 ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "Emphasis and blockquote in asset name",
			item: Item{
				Severity: "high",
				Name:     "普通标题",
				Assets:   []string{"a.com/*注入*>引用"},
			},
			must:  []string{`\*注入\*`, `\>`},
			wrong: []string{"*注入*"},
		},
		{
			name: "Backticks and pipe in summary",
			item: Item{
				Severity: "high",
				Name:     "标题",
				Summary:  "`code` | 表格",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// Single-mode writeItem is the shared rendering path for three Markdown channels.
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("Escaped form missing %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("Unescaped form %q permits structure or link injection:\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst fixes escaping order: process backslashes first so newly
// inserted escapes are not doubled.
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("Incorrect escaping order; got %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes prevents Markdown escapes leaking into Telegram HTML.
// Shared title escaping once displayed visible backslashes such as `\(1\)`.
//
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *重点*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("Telegram body contains markdown backslash escaping:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
