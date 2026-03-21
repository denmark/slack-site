package msghtml

import (
	"strings"
	"testing"
)

func TestTruncateText_noTruncation(t *testing.T) {
	s := "  hello world  "
	got := TruncateText(s, 100)
	if got != "hello world" {
		t.Errorf("got %q; want trimmed full string", got)
	}
}

func TestTruncateText_empty(t *testing.T) {
	if got := TruncateText("   ", 5); got != "" {
		t.Errorf("got %q; want empty", got)
	}
}

func TestTruncateText_maxZero(t *testing.T) {
	if got := TruncateText("abc", 0); got != "..." {
		t.Errorf("max 0 non-empty: got %q; want ...", got)
	}
	if got := TruncateText("", 0); got != "" {
		t.Errorf("max 0 empty: got %q", got)
	}
}

func TestTruncateText_truncatesRunes(t *testing.T) {
	got := TruncateText("abcd", 3)
	if !strings.HasSuffix(got, "...") || !strings.HasPrefix(got, "abc") {
		t.Errorf("got %q", got)
	}
}

func TestTruncateText_utf8Runes(t *testing.T) {
	s := "日本語xyz"
	got := TruncateText(s, 3)
	want := "日本語..."
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestTruncateText_closesOpenTags(t *testing.T) {
	got := TruncateText("<b>hello there</b>", 8)
	// 8 runes might cut inside "there" — result should balance tags
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected suffix ...: %q", got)
	}
	if !strings.Contains(got, "</b>") {
		t.Errorf("should close b tag before ellipsis: %q", got)
	}
}

func TestTruncateText_voidElement(t *testing.T) {
	got := TruncateText("<br>ab", 3)
	// runes: < b r > a b — need to check actual cut; at least should not stack br as closable
	if strings.Contains(got, "</br>") {
		t.Errorf("br is void, no close: %q", got)
	}
}

func TestTruncateText_selfClosing(t *testing.T) {
	got := TruncateText("<img src='x'/>abc", 5)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("%q", got)
	}
}

func TestTruncateText_extendsPastIncompleteTag(t *testing.T) {
	// Cut lands inside an opening tag; extendCutPastIncompleteTag should include through '>'
	s := "aa<a href=\"http://x.com\">b"
	got := TruncateText(s, 4)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("%q", got)
	}
}

func TestTruncateText_incompleteTagDropped(t *testing.T) {
	// No closing '>' after cut — opener removed
	s := strings.Repeat("a", 20) + "<bad"
	got := TruncateText(s, 10)
	if strings.Contains(got, "<bad") {
		t.Errorf("incomplete tag should be dropped: %q", got)
	}
}

func TestHtmlClosingTags_nested(t *testing.T) {
	prefix := "<div><span>in"
	got := htmlClosingTags(prefix)
	want := "</span></div>"
	if got != want {
		t.Errorf("htmlClosingTags(%q) = %q; want %q", prefix, got, want)
	}
}

func TestHtmlClosingTags_closingTagPops(t *testing.T) {
	prefix := "<div></div><span>x"
	got := htmlClosingTags(prefix)
	if got != "</span>" {
		t.Errorf("got %q; want </span>", got)
	}
}

func TestHtmlClosingTags_voidBr(t *testing.T) {
	if got := htmlClosingTags("<br><div>x"); got != "</div>" {
		t.Errorf("got %q", got)
	}
}

func TestPopMatchingTag(t *testing.T) {
	st := []string{"div", "span", "p"}
	st = popMatchingTag(st, "span")
	if len(st) != 1 || st[0] != "div" {
		t.Errorf("pop span should leave div only: %v", st)
	}
	st = popMatchingTag([]string{"a", "b"}, "missing")
	if len(st) != 2 {
		t.Errorf("no match: stack unchanged")
	}
}
