// This file renders `rc run thread <id> --transcript`: one chat conversation as compact Markdown,
// in reading order. Markdown, not a table: a question and an answer are multi-line bodies, and a table
// cell would either mangle them or truncate them into uselessness.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

// Transcript writes the conversation: a header with the honest counts, one block per turn (folded form
// answers indented under the turn they answer), and a footer repeating the counts so a long dump still
// ends with "what did I just read".
func Transcript(w io.Writer, t digest.Transcript) {
	_, _ = fmt.Fprintf(w, "# Conversation %s\n\n", orDash(t.SessionID, "(unknown session)"))
	_, _ = fmt.Fprintf(w, "%d turns · %s · %s\n", len(t.Turns), countLabel(t.Questions, "question"), countLabel(t.FormAnswers, "form answer"))
	if t.AnswersTruncated {
		_, _ = fmt.Fprintf(w, "\n> Long conversation: answers are clipped to their first %d characters. Read one turn in full with `rc run trace <run-id> --brief`.\n",
			digest.TranscriptAnswerHeadChars)
	}
	if len(t.Turns) == 0 {
		_, _ = fmt.Fprintln(w, "\n_No runs in this conversation._")
		return
	}
	for _, turn := range t.Turns {
		if turn.IsFormAnswer {
			transcriptFormAnswer(w, turn)
			continue
		}
		transcriptTurn(w, turn)
	}
	_, _ = fmt.Fprintf(w, "\n---\nquestions: %d, form answers: %d\n", t.Questions, t.FormAnswers)
}

func transcriptTurn(w io.Writer, turn digest.TranscriptTurn) {
	_, _ = fmt.Fprintf(w, "\n## Turn %d · %s%s\n", turn.Seq, orDash(turn.CreatedAt, "-"), principalSuffix(turn.PrincipalID))
	if q := strings.TrimSpace(turn.Question); q != "" {
		_, _ = fmt.Fprintf(w, "\n**Q**\n%s\n", q)
	}
	if a := strings.TrimSpace(turn.Answer); a != "" {
		_, _ = fmt.Fprintf(w, "\n**A**\n%s\n", a)
	} else if turn.Unavailable == "" {
		_, _ = fmt.Fprint(w, "\n**A**\n_(none placed)_\n")
	}
	_, _ = fmt.Fprintf(w, "\n%s\n", turnFooter(turn))
}

// transcriptFormAnswer renders a clarification-form submission folded under the turn it answers: one
// indented `↳ formulier:` line plus whatever the engine replied, so the reader sees the round trip
// without counting it as a new question.
func transcriptFormAnswer(w io.Writer, turn digest.TranscriptTurn) {
	_, _ = fmt.Fprintf(w, "\n  ↳ formulier: %s\n", oneLine(turn.Question))
	if a := strings.TrimSpace(turn.Answer); a != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", indentLines(a, "  "))
	}
	_, _ = fmt.Fprintf(w, "\n  %s\n", turnFooter(turn))
}

// turnFooter is the one italic line with the turn's engine-side facts: outcome, human feedback, the run
// page, and any reason the bodies are missing.
func turnFooter(turn digest.TranscriptTurn) string {
	parts := []string{"outcome: " + orDash(turn.Outcome, "-")}
	if fb := transcriptFeedbackLabel(turn.Feedback); fb != "" {
		parts = append(parts, "feedback: "+fb)
	}
	if turn.Unavailable != "" {
		parts = append(parts, "unavailable: "+turn.Unavailable)
	}
	if turn.RunURL != "" {
		parts = append(parts, turn.RunURL)
	} else {
		parts = append(parts, "run "+turn.RunID)
	}
	return "_" + strings.Join(parts, " · ") + "_"
}

func transcriptFeedbackLabel(f *digest.TranscriptFeedback) string {
	if f == nil {
		return ""
	}
	var parts []string
	if f.Score != nil {
		parts = append(parts, fmt.Sprintf("%d★", *f.Score))
	}
	if c := oneLine(f.Comment); c != "" {
		parts = append(parts, "“"+c+"”")
	}
	return strings.Join(parts, " ")
}

func principalSuffix(id string) string {
	if id == "" {
		return ""
	}
	return " · principal " + id
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
