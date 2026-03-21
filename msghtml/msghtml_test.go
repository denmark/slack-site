package msghtml

import (
	"strings"
	"testing"

	"github.com/denmark/slack-site/models"
)

func TestRender_nil(t *testing.T) {
	if got := Render(nil); got != "" {
		t.Errorf("Render(nil) = %q; want empty", got)
	}
}

func TestRender_plainText(t *testing.T) {
	msg := &models.Message{Text: `a <b> & "quotes"`}
	want := `a &lt;b&gt; &amp; &#34;quotes&#34;`
	if got := Render(msg); got != want {
		t.Errorf("Render() = %q; want %q", got, want)
	}
}

func TestRender_blocksEmptyFallsBackToText(t *testing.T) {
	msg := &models.Message{
		Text: "fallback",
		Blocks: []interface{}{
			map[string]interface{}{"type": "section", "text": map[string]interface{}{"type": "plain_text", "text": "ignored"}},
		},
	}
	if got := Render(msg); got != "fallback" {
		t.Errorf("non-rich_text blocks should fall back to Text: got %q", got)
	}
}

func TestRender_richTextSection(t *testing.T) {
	msg := &models.Message{
		Text: "ignored",
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{"type": "text", "text": "Hello "},
							map[string]interface{}{
								"type": "text",
								"text": "world",
								"style": map[string]interface{}{
									"bold": true, "italic": true, "code": true, "strike": true,
								},
							},
						},
					},
				},
			},
		},
	}
	got := Render(msg)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "<b>") {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestRender_preformatted(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_preformatted",
						"elements": []interface{}{
							map[string]interface{}{"type": "text", "text": "line1\nline2"},
						},
					},
				},
			},
		},
	}
	got := Render(msg)
	if !strings.HasPrefix(strings.TrimSpace(got), "<pre>") || !strings.Contains(got, "</pre>") {
		t.Errorf("want pre wrapper: %q", got)
	}
}

func TestRender_preformattedEmptyOmitted(t *testing.T) {
	msg := &models.Message{
		Text: "plain",
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type":     "rich_text_preformatted",
						"elements": []interface{}{},
					},
				},
			},
		},
	}
	if got := Render(msg); got != "plain" {
		t.Errorf("empty pre should fall back to text: got %q", got)
	}
}

func TestRender_richTextList(t *testing.T) {
	// rich_text_list passes elements to renderInlineElements (Slack list items are often text nodes).
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_list",
						"elements": []interface{}{
							map[string]interface{}{"type": "text", "text": "item"},
						},
					},
				},
			},
		},
	}
	if got := Render(msg); got != "item" {
		t.Errorf("got %q; want item", got)
	}
}

func TestRender_quote(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_quote",
						"elements": []interface{}{
							map[string]interface{}{"type": "text", "text": "cited"},
						},
					},
				},
			},
		},
	}
	got := Render(msg)
	if !strings.Contains(got, "<blockquote>") {
		t.Errorf("want blockquote: %q", got)
	}
}

func TestRender_link(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{
								"type": "link",
								"url":  "https://e.com/x?a=1&b=2",
								"text": `say "hi"`,
							},
							map[string]interface{}{
								"type": "link",
								"url":  "https://bare.com/",
							},
						},
					},
				},
			},
		},
	}
	got := Render(msg)
	if !strings.Contains(got, `href="https://e.com/x?a=1&amp;b=2"`) {
		t.Errorf("escaped href missing: %q", got)
	}
	if !strings.Contains(got, `say &#34;hi&#34;`) {
		t.Errorf("escaped link text missing: %q", got)
	}
	if !strings.Contains(got, `>https://bare.com/</a>`) {
		t.Errorf("bare link should use URL as text: %q", got)
	}
}

func TestRender_linkEmptyURL(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{"type": "link", "url": "", "text": "x"},
						},
					},
				},
			},
		},
	}
	if got := Render(msg); got != "" {
		t.Errorf("empty url should render nothing: %q", got)
	}
}

func TestRender_emojiUserChannelBroadcast(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{"type": "emoji", "name": "wave"},
							map[string]interface{}{"type": "user", "user_id": "U123"},
							map[string]interface{}{"type": "channel", "channel_id": "C456"},
							map[string]interface{}{"type": "broadcast", "range": "here"},
							map[string]interface{}{"type": "broadcast"},
						},
					},
				},
			},
		},
	}
	got := Render(msg)
	for _, sub := range []string{"wave", `data-user-id="U123"`, `data-channel-id="C456"`, `data-range="here"`, `data-range="channel"`} {
		if !strings.Contains(got, sub) {
			t.Errorf("expected %q in %q", sub, got)
		}
	}
}

func TestRender_unknownInlineIgnored(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{"type": "unknown_thing"},
							map[string]interface{}{"type": "text", "text": "ok"},
						},
					},
				},
			},
		},
	}
	if got := Render(msg); got != "ok" {
		t.Errorf("got %q; want ok", got)
	}
}

func TestRender_nonMapBlockSkipped(t *testing.T) {
	msg := &models.Message{
		Text:   "plain",
		Blocks: []interface{}{"not-a-map", 42},
	}
	if got := Render(msg); got != "plain" {
		t.Errorf("got %q; want plain", got)
	}
}

func TestRender_trimSpace(t *testing.T) {
	msg := &models.Message{
		Blocks: []interface{}{
			map[string]interface{}{
				"type": "rich_text",
				"elements": []interface{}{
					map[string]interface{}{
						"type": "rich_text_section",
						"elements": []interface{}{
							map[string]interface{}{"type": "text", "text": "  x  "},
						},
					},
				},
			},
		},
	}
	if got := Render(msg); got != "x" {
		t.Errorf("got %q; want trimmed x", got)
	}
}
