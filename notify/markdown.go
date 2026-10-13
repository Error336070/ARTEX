package notify

import (
	"fmt"
	"strings"
)

// Shared Markdown rendering for DingTalk and WeCom; Feishu uses card JSON, Telegram HTML, and
// email HTML in their adapters. maxAssetsShown limits displayed assets: listing dozens overwhelms

// messages with little useful information beyond the first few.
//
const maxAssetsShown = 3

// maxSummaryRunes limits summary characters. Messaging directs readers to platform details; it is
// not the full report.
const maxSummaryRunes = 120

// markdownReservedBytes reserves digest headers, severity distribution, possible truncation
// notices, and dashboard footer. Deduct this before packing whole entries so readers can identify
// the batch and unseen remainder.
const markdownReservedBytes = 320

// markdownEscape escapes structural Markdown characters. Titles, summaries, types, and asset names
// are untrusted target/model data; scanned URLs include target-controlled queries. A title
// containing a new external link could render as a clickable link, while
// `![](http://attacker.tld/beacon)` triggers a client fetch revealing that it was read and the
// reader's IP. Even benign bold/quote formatting can push serious findings below collapsed
// content. Escape heading/link/emphasis/list/quote/strikethrough characters and process
// backslashes first to avoid re-escaping inserted escapes.
//
//
//
//
//
//
//
//
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText normalises untrusted text to one line and escapes it. Newlines can forge list/quote
// structure even when metacharacters are escaped.
//
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle returns unescaped source text for platform titles. It is shared across Markdown,
// Telegram HTML, Feishu plain_text, Webhook JSON, and mail subjects, each requiring different
// escaping. Output renderers own escaping: writeItem / feishuItemLines / telegramEscape. Shared
// Markdown escaping previously exposed literal backslashes such as `\(1\)` in Telegram.
//
//
//
//
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("Finding summary · %d findings", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "Finding notification"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody returns body and actual included entry count. Mark only the first kept entries
// delivered; entries excluded by length limits remain for the next batch. Otherwise truncation
// silently loses entries while history reports full delivery. maxBytes<=0 means unlimited.
//
//
//
//
//
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		// Send oversized single messages with final truncation: partial finding information is preferable
		// to none.
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[View all in the platform](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro renders window, count, and severity distribution so recipients can assess
// urgency without opening the platform. items contains entries actually included; total is the
// complete batch. Explicitly state how many continue in the next message so readers do not mistake
// the displayed count for the entire batch.
//
//
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**Last %d minutes: %d new findings**", m.WindowMinutes, total)
	} else {
		fmt.Fprintf(&b, "**%d new findings**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, " (showing the first %d; remaining %d continue in the next message)", len(items), extra)
	}
	// Severity distribution counts only included entries, matching the findings visible below.
	//
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem renders one finding. prefix numbers digest entries; single=true includes
// summary/backlink, otherwise one-line summaries keep 50-entry digests manageable. External
// titles/types/assets/summaries pass through markdownText. Admin public_base_url backlinks remain
// clickable and are output unchanged.
//
//
//
//
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		// Digest mode shows one line with compressed assets and summary.
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " — " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**Status changed**: %s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**Class**: %s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**Assets**: %s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**Summary**: %s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[View details](%s)\n", it.DetailURL)
	}
}
