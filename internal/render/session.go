// This file renders the ONE artifact `rc run session` writes: a markdown file joining a shared chat
// session's transcript to the runs behind it, each row ending in the drill command for that run.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// SessionMarkdown writes the shared-session artifact. It is deliberately ONE flat document, not a
// second progressive-disclosure format: the per-run depth already lives behind `rc run debug`, which
// each table row spells out as a copy-pasteable command.
func SessionMarkdown(w io.Writer, s *client.SharedSession, t *client.SharedTranscript) {
	_, _ = fmt.Fprintf(w, "# %s\n\n", sessionTitle(s, t))
	if s.SessionID != "" {
		_, _ = fmt.Fprintf(w, "- Session: %s\n", s.SessionID)
	}
	if s.ID != "" && s.ID != s.SessionID {
		_, _ = fmt.Fprintf(w, "- Resolved: %s (%s)\n", s.ID, orDash(s.ResolvedBy, "-"))
	}
	_, _ = fmt.Fprintf(w, "- Runs: %d\n", len(s.Runs))

	_, _ = fmt.Fprint(w, "\n## Transcript\n\n")
	if len(t.Messages) == 0 {
		_, _ = fmt.Fprint(w, "_No messages._\n\n")
	}
	for _, m := range t.Messages {
		_, _ = fmt.Fprintf(w, "### %s%s\n\n", orDash(m.Role, "message"), messageStamp(m))
		for _, line := range partLines(m.Parts) {
			_, _ = fmt.Fprintf(w, "%s\n\n", line)
		}
	}

	_, _ = fmt.Fprint(w, "## Runs\n\n")
	if len(s.Runs) == 0 {
		_, _ = fmt.Fprint(w, "_No runs behind this session._\n")
		return
	}
	_, _ = fmt.Fprint(w, "| RUN | STATUS | OUTCOME | CREATED | TOPIC | DRILL |\n")
	_, _ = fmt.Fprint(w, "|---|---|---|---|---|---|\n")
	for _, r := range s.Runs {
		_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n",
			mdCell(r.RunID), mdCell(r.Status), mdCell(r.Outcome), mdCell(r.CreatedAt),
			mdCell(orDash(r.Topic, "-")), drillCommand(r))
	}
}

// sessionTitle prefers the transcript's title: it is the session's own, while the runs envelope may be
// carrying a fallback.
func sessionTitle(s *client.SharedSession, t *client.SharedTranscript) string {
	if title := strings.TrimSpace(t.Title); title != "" {
		return title
	}
	if title := strings.TrimSpace(s.Title); title != "" {
		return title
	}
	return "Shared session"
}

// messageStamp renders a timestamp only when the server sent one: MessageWire's ordering fact is `seq`,
// and inventing a time would be worse than omitting it.
func messageStamp(m client.TranscriptMessage) string {
	if m.CreatedAt == "" {
		return ""
	}
	return " · " + m.CreatedAt
}

// partLines projects one message's parts into reading order. Anything that is not prose degrades to a
// one-line placeholder rather than vanishing, so a card type we don't know about still shows up.
func partLines(parts []client.TranscriptPart) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if text := strings.TrimSpace(p.Text); text != "" {
				out = append(out, text)
			}
		case "file":
			out = append(out, "`[file: "+orDash(firstNonEmpty(p.Filename, p.Title), "unnamed")+"]`")
		default:
			out = append(out, "`[card: "+orDash(p.Type, "unknown")+"]`")
		}
	}
	if len(out) == 0 {
		out = append(out, "_(empty)_")
	}
	return out
}

// drillCommand is the per-row escape hatch into the full run bundle. Without a share_url the reader has
// no credential for that run, so the cell says so instead of printing a command that would 404.
func drillCommand(r client.RunSummary) string {
	if r.ShareURL == "" {
		return "-"
	}
	return "`rc run debug '" + r.ShareURL + "'`"
}

// mdCell keeps a value inside its table cell: a raw pipe would silently add a column.
func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
