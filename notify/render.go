package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes limits s to max bytes without splitting UTF-8 characters. WeCom caps Markdown at
// 4096 bytes, not characters; Chinese characters commonly use three bytes, so raw slicing produces
// invalid UTF-8, rejected messages, or garbled display. Retreat from the budget boundary to the
// nearest rune start using utf8.RuneStart (continuation bytes 0b10xxxxxx). max<=0 is unlimited.
// Append an ellipsis unless the budget is too small for it.
//
//
//
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		// If max is smaller than the ellipsis, omit it to avoid exceeding max.
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine folds all whitespace and truncates by characters for message titles; summary newlines
// would otherwise break tables/title layouts. max<=0 is unlimited.
//
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes limits characters, not bytes, with an ellipsis; max<=0 is unlimited. WeCom uses
// bytes and Telegram characters. The wrong measure silently shortens Chinese content (4096 bytes
// gives about 1365 characters), so retain both functions and select per channel.
//
//
//
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML limits character count without partial tags. Plain truncation can leave `<a
// href="htt`, causing rejection or swallowing later text into attributes. After character
// truncation, retreat before an unmatched trailing `<`. Do not balance tags: Telegram closes them
// automatically, while implementing quote/comment/self-closing-tag handling would add
// disproportionate complexity.
//
//
//
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	// Retreat before a trailing `<` without a following `>`.
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	// Also retreat before partial HTML entities such as `&amp` cut from `&amp;`, which could cause an
	// entity-only parser to reject the whole message. Oversized digests are common and should not lose
	// complete notifications this way.
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount counts whole entries fitting the budget. Rendering everything and truncating
// silently loses trailing findings while history marks them delivered. Whole-entry packing retains
// leftovers for the next batch; kept is the actual delivered count. maxSize<=0 is unlimited;
// reserve budgets headers/footers; size uses channel units (bytes for WeCom/DingTalk, characters
// for Telegram); render measures actual variable entry text. Return at least one when entries
// exist, using final truncation for extreme oversized entries rather than permanently blocking the
// batch.
//
//
//
//
//
//
//
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize name packItemCount measurement units so callers clearly choose bytes or
// characters rather than anonymous func(s string) int closures.
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine renders one line, omitting assets beyond limit with a total count; dozens of anchored
// assets would overwhelm messages.
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, "、")
	}
	return strings.Join(assets[:limit], ", ") + " of " + itoa(len(assets)) + " total"
}

// itoa abbreviates strconv.Itoa for display concatenation without repeated imports.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
