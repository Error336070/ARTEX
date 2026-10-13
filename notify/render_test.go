package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// Core invariant: WeCom limits bytes, while Chinese characters use three bytes. Raw slicing
	// creates invalid UTF-8 and platform rejection. Mixed Chinese/English inputs of coprime lengths
	// exercise every possible boundary.
	inputs := []string{
		"中文测试内容",
		"混合 mixed 内容 content",
		"a中b文c测d试e",
		"🔴🟠🟡🔵", // Four-byte emoji makes incorrect splitting more obvious.
		strings.Repeat("漏洞", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("Input %q max=%d: invalid UTF-8 output %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("Input %q max=%d: output of %d bytes exceeds limit", in, max, len(got))
			}
			// Untruncated content must remain unchanged.
			if len(in) <= max && got != in {
				t.Fatalf("Input %q max=%d: content changed within limit -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 should mean unlimited")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 should mean unlimited")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// When max is smaller than the ellipsis, adding it must not exceed the limit.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1: output %q length %d exceeds limit", got, len(got))
	}
	// Normal truncation includes an ellipsis.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("Expected ellipsis; got %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// Retain character versus byte distinction: Telegram counts characters; bytes reduce Chinese
	// messages to roughly one third.
	s := "一二三四五六七八九十"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("Expected 5 characters; got %d (%q)", n, got)
	}
	// The same string truncated by bytes should be much shorter.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("Byte and rune limits should produce different character counts")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("第一行\n\n第二行\t带制表   多空格", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("All whitespace should be collapsed; got %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("Consecutive spaces should be collapsed; got %q", got)
	}
	// Truncated output remains readable and valid.
	got = OneLine("一二三四五六七八九十", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("Expected 4 characters; got %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Plain HTML truncation can leave `<a href="htt` and cause whole-message rejection.
	s := `<b>标题</b>正文正文正文<a href="https://example.com/very/long/path">查看详情</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: output of %d characters exceeds limit", max, n)
		}
		// No unmatched trailing `<` without a following `>`.
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: trailing tag truncated -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("No assets should return empty string; got %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a、b" {
		t.Fatalf("All assets should be listed within limit; got %q", got)
	}
	// Over-limit asset lists must state the total so readers know how many are omitted.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "of 5 total") {
		t.Fatalf("Total count 5 should be shown; got %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("Empty severity has rank 0 and should fail any threshold")
	}
	if !AtLeast("critical", "") {
		t.Fatal("Empty threshold should allow")
	}
	if got := StatusLabel("fixed"); got != "Fixed" {
		t.Fatalf("Incorrect status mapping; got %q", got)
	}
	// Unknown statuses are echoed unchanged; do not invent labels.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("Unknown status should be preserved; got %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity covers an audit gap: avoid partial entities as well as tags.
// Cutting `&amp;` to `&amp` can make entity-only parsers reject complete messages; oversized
// digests are common, making this unacceptable.
//
//
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// No trailing entity fragment with & but no matching ;.
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: partial trailing entity %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: malformed entity", max)
		}
	}
}
