package notify

import (
	"context"
	"net/http"
	"strings"
)

// slack posts Block Kit to an incoming webhook. The webhook is bound to
// its channel. Mattermost and Rocket.Chat take the same payload.
type slack struct{ url string }

func (s *slack) Send(ctx context.Context, m Message) error {
	return postJSON(ctx, s.url, slackPayload(m), nil)
}

func slackPayload(m Message) map[string]any {
	blocks := []any{
		map[string]any{"type": "header", "text": map[string]any{
			"type": "plain_text", "text": truncate(m.Severity.emoji()+" "+m.Title, 150), "emoji": true,
		}},
	}
	if m.Summary != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": mrkdwn(truncate(slackEscape(m.Summary), 3000))})
	}
	if len(m.Fields) > 0 {
		fields := []any{}
		for _, f := range m.Fields[:min(len(m.Fields), 10)] {
			fields = append(fields, mrkdwn(truncate("*"+slackEscape(f.Label)+"*\n"+slackEscape(f.Value), 2000)))
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	if m.URL != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []any{
			mrkdwn("<" + slackEscape(m.URL) + "|Open in Echowa>"),
		}})
	}

	// text is what the notification itself shows.
	return map[string]any{"text": truncate(m.Title+": "+m.Summary, 3000), "blocks": blocks}
}

func mrkdwn(text string) map[string]any {
	return map[string]any{"type": "mrkdwn", "text": text}
}

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func slackEscape(s string) string { return slackEscaper.Replace(s) }

// discord posts an embed, coloured by severity, to a channel webhook.
type discord struct{ url string }

func (d *discord) Send(ctx context.Context, m Message) error {
	return postJSON(ctx, d.url, discordPayload(m), nil)
}

func discordPayload(m Message) map[string]any {
	embed := map[string]any{
		"title":       truncate(m.Title, 256),
		"description": truncate(m.Summary, 4096),
		"color":       discordColour(m.Severity),
	}
	if m.URL != "" {
		embed["url"] = m.URL
	}
	if len(m.Fields) > 0 {
		fields := []any{}
		for _, f := range m.Fields[:min(len(m.Fields), 25)] {
			fields = append(fields, map[string]any{"name": truncate(f.Label, 256), "value": truncate(f.Value, 1024), "inline": true})
		}
		embed["fields"] = fields
	}

	return map[string]any{"username": "Echowa", "embeds": []any{embed}}
}

func discordColour(s Severity) int {
	switch s {
	case Critical:
		return 0xE5484D
	case Warning:
		return 0xF5A524
	case Resolved:
		return 0x30A46C
	}

	return 0x3B82F6
}

// teams posts an Adaptive Card to a Workflows webhook ("When a Teams
// webhook request is received"). Office 365 connectors are retired.
type teams struct{ url string }

func (t *teams) Send(ctx context.Context, m Message) error {
	return postJSON(ctx, t.url, teamsPayload(m), nil)
}

func teamsPayload(m Message) map[string]any {
	body := []any{
		map[string]any{"type": "TextBlock", "text": m.Title, "weight": "Bolder", "size": "Medium", "wrap": true, "color": teamsColour(m.Severity)},
	}
	if m.Summary != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Summary, "wrap": true})
	}
	if len(m.Fields) > 0 {
		facts := []any{}
		for _, f := range m.Fields {
			facts = append(facts, map[string]any{"title": f.Label, "value": f.Value})
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    body,
	}
	if m.URL != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": "Open in Echowa", "url": m.URL}}
	}

	return map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content":     card,
		}},
	}
}

func teamsColour(s Severity) string {
	switch s {
	case Critical:
		return "Attention"
	case Warning:
		return "Warning"
	case Resolved:
		return "Good"
	}

	return "Default"
}

// ntfy publishes JSON to the server's root, which keeps the title out of
// headers, where it would have to be ASCII.
type ntfy struct {
	server string
	topic  string
	token  string
}

func (n *ntfy) Send(ctx context.Context, m Message) error {
	var header http.Header
	if n.token != "" {
		header = http.Header{"Authorization": {"Bearer " + n.token}}
	}

	return postJSON(ctx, strings.TrimSuffix(n.server, "/")+"/", ntfyPayload(n.topic, m), header)
}

func ntfyPayload(topic string, m Message) map[string]any {
	lines := []string{m.Summary}
	if len(m.Fields) > 0 {
		lines = append(lines, "")
		for _, f := range m.Fields {
			lines = append(lines, f.Label+": "+f.Value)
		}
	}
	payload := map[string]any{
		"topic":    topic,
		"title":    m.Title,
		"message":  truncate(strings.Join(lines, "\n"), 4000),
		"priority": ntfyPriority(m.Severity),
		"tags":     []string{ntfyTag(m.Severity)},
	}
	if m.URL != "" {
		payload["click"] = m.URL
	}

	return payload
}

// ntfyPriority: 5 is urgent, 3 the default.
func ntfyPriority(s Severity) int {
	switch s {
	case Critical:
		return 5
	case Warning:
		return 4
	}

	return 3
}

// ntfyTag is an emoji short code, which ntfy shows before the title.
func ntfyTag(s Severity) string {
	switch s {
	case Critical:
		return "rotating_light"
	case Warning:
		return "warning"
	case Resolved:
		return "white_check_mark"
	}

	return "information_source"
}
