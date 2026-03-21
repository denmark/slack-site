package msghtml

import (
	"strings"
	"unicode/utf8"
)

// voidHTMLTags are HTML void elements; they must not be pushed onto the open-tag stack.
var voidHTMLTags = map[string]struct{}{
	"area": {}, "base": {}, "br": {}, "col": {}, "embed": {}, "hr": {}, "img": {},
	"input": {}, "link": {}, "meta": {}, "param": {}, "source": {}, "track": {}, "wbr": {},
}

// TruncateText trims s, then truncates after max runes of content. If truncated, balances
// open HTML tags in the prefix and appends "...".
func TruncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	cut, truncated := truncateCutAtRunes(s, max)
	if !truncated {
		return s
	}
	cut = extendCutPastIncompleteTag(s, cut)
	prefix := s[:cut]
	return prefix + htmlClosingTags(prefix) + "..."
}

func truncateCutAtRunes(s string, max int) (cut int, truncated bool) {
	if max <= 0 {
		return 0, s != ""
	}
	runeCount := 0
	for i := 0; i < len(s); {
		if runeCount == max {
			return i, true
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		if w == 0 {
			break
		}
		i += w
		runeCount++
	}
	return len(s), false
}

func extendCutPastIncompleteTag(s string, cut int) int {
	inTag := false
	var quote byte
	for i := 0; i < cut; i++ {
		c := s[i]
		if !inTag {
			if c == '<' {
				inTag = true
				quote = 0
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			inTag = false
		}
	}
	if !inTag {
		return cut
	}
	j := cut
	for j < len(s) {
		c := s[j]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			j++
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			j++
			continue
		}
		if c == '>' {
			return j + 1
		}
		j++
	}
	// No closing '>' — drop the incomplete tag opener so we do not emit broken markup.
	for k := cut - 1; k >= 0; k-- {
		if s[k] == '<' {
			return k
		}
	}
	return cut
}

func skipToTagEnd(s string, start int) int {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return i + 1
		}
	}
	return len(s)
}

func parseTagName(s string, i *int) string {
	start := *i
	for *i < len(s) && tagNameByte(s[*i]) {
		*i++
	}
	return strings.ToLower(s[start:*i])
}

func tagNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func tagIsSelfClosingBeforeGT(s string, gt int) bool {
	j := gt - 1
	for j >= 0 && isSpaceByte(s[j]) {
		j--
	}
	return j >= 0 && s[j] == '/'
}

func htmlClosingTags(prefix string) string {
	var stack []string
	i := 0
	for i < len(prefix) {
		if prefix[i] != '<' {
			i++
			continue
		}
		start := i
		i++
		if i >= len(prefix) {
			break
		}
		if prefix[i] == '/' {
			i++
			name := parseTagName(prefix, &i)
			i = skipToTagEnd(prefix, i)
			stack = popMatchingTag(stack, name)
			continue
		}
		if prefix[i] == '!' || prefix[i] == '?' {
			i = skipToTagEnd(prefix, i+1)
			continue
		}
		name := parseTagName(prefix, &i)
		if name == "" {
			i++
			continue
		}
		end := skipToTagEnd(prefix, i)
		if end <= start {
			break
		}
		gt := end - 1
		if _, void := voidHTMLTags[name]; void || tagIsSelfClosingBeforeGT(prefix, gt) {
			i = end
			continue
		}
		stack = append(stack, name)
		i = end
	}
	var b strings.Builder
	for j := len(stack) - 1; j >= 0; j-- {
		b.WriteString("</")
		b.WriteString(stack[j])
		b.WriteString(">")
	}
	return b.String()
}

func popMatchingTag(stack []string, name string) []string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == name {
			return stack[:i]
		}
	}
	return stack
}
