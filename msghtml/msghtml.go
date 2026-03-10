package msghtml

import (
	"html"
	"strings"

	"github.com/denmark/slack-site/models"
)

// Render returns the message body as HTML. If the message has rich_text blocks,
// they are rendered to HTML (links as <a>, bold/code/italic/strike wrapped in
// appropriate tags). Otherwise the plain Text field is escaped and returned.
func Render(msg *models.Message) string {
	if msg == nil {
		return ""
	}
	if len(msg.Blocks) > 0 {
		if h := renderBlocks(msg.Blocks); h != "" {
			return strings.TrimSpace(h)
		}
	}
	return html.EscapeString(msg.Text)
}

func renderBlocks(blocks []interface{}) string {
	var out strings.Builder
	for _, b := range blocks {
		m, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "rich_text" {
			continue
		}
		elems, _ := m["elements"].([]interface{})
		out.WriteString(renderTopElements(elems))
	}
	return out.String()
}

func renderTopElements(elems []interface{}) string {
	var out strings.Builder
	for _, e := range elems {
		out.WriteString(renderBlockElement(e))
	}
	return out.String()
}

func renderBlockElement(e interface{}) string {
	m, ok := e.(map[string]interface{})
	if !ok {
		return ""
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "rich_text_section":
		return renderInlineElements(m["elements"])
	case "rich_text_preformatted":
		inner := renderInlineElements(m["elements"])
		if inner == "" {
			return ""
		}
		return "<pre>" + inner + "</pre>"
	case "rich_text_list":
		// optional: render list items
		return renderInlineElements(m["elements"])
	case "rich_text_quote":
		inner := renderInlineElements(m["elements"])
		if inner == "" {
			return ""
		}
		return "<blockquote>" + inner + "</blockquote>"
	default:
		return ""
	}
}

func renderInlineElements(elems interface{}) string {
	list, ok := elems.([]interface{})
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, e := range list {
		out.WriteString(renderInlineElement(e))
	}
	return out.String()
}

func renderInlineElement(e interface{}) string {
	m, ok := e.(map[string]interface{})
	if !ok {
		return ""
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "text":
		return renderTextElement(m)
	case "link":
		return renderLinkElement(m)
	case "emoji":
		return renderEmojiElement(m)
	case "user":
		return renderUserElement(m)
	case "channel":
		return renderChannelElement(m)
	case "broadcast":
		return renderBroadcastElement(m)
	default:
		return ""
	}
}

func renderTextElement(m map[string]interface{}) string {
	text, _ := m["text"].(string)
	if text == "" {
		return ""
	}
	text = html.EscapeString(text)
	style, _ := m["style"].(map[string]interface{})
	if style != nil {
		if bold, _ := style["bold"].(bool); bold {
			text = "<b>" + text + "</b>"
		}
		if code, _ := style["code"].(bool); code {
			text = "<code>" + text + "</code>"
		}
		if italic, _ := style["italic"].(bool); italic {
			text = "<em>" + text + "</em>"
		}
		if strike, _ := style["strike"].(bool); strike {
			text = "<s>" + text + "</s>"
		}
	}
	return text
}

func renderLinkElement(m map[string]interface{}) string {
	url, _ := m["url"].(string)
	if url == "" {
		return ""
	}
	url = html.EscapeString(url)
	text, _ := m["text"].(string)
	if text == "" {
		text = url
	} else {
		text = html.EscapeString(text)
	}
	return "<a href=\"" + url + "\">" + text + "</a>"
}

func renderEmojiElement(m map[string]interface{}) string {
	name, _ := m["name"].(string)
	if name == "" {
		return ""
	}
	return html.EscapeString(name)
}

func renderUserElement(m map[string]interface{}) string {
	// Optional: could look up user and show name; for now emit placeholder
	id, _ := m["user_id"].(string)
	if id == "" {
		return ""
	}
	return "<span class=\"slack-user\" data-user-id=\"" + html.EscapeString(id) + "\">@" + html.EscapeString(id) + "</span>"
}

func renderChannelElement(m map[string]interface{}) string {
	id, _ := m["channel_id"].(string)
	if id == "" {
		return ""
	}
	return "<span class=\"slack-channel\" data-channel-id=\"" + html.EscapeString(id) + "\">#" + html.EscapeString(id) + "</span>"
}

func renderBroadcastElement(m map[string]interface{}) string {
	rangeVal, _ := m["range"].(string)
	if rangeVal == "" {
		rangeVal = "channel"
	}
	return "<span class=\"slack-broadcast\" data-range=\"" + html.EscapeString(rangeVal) + "\">@" + html.EscapeString(rangeVal) + "</span>"
}
