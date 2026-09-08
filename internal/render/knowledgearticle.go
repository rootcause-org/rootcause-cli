// View for `rc project knowledge article apply`: one terse verdict line, plus — on a dry run — the
// exact provider calls that WOULD be sent, so the operator reviews the write before it happens.

package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// KnowledgeArticleApply prints the verdict line and, for a dry run, every pending provider request
// with its body pretty-printed in FULL — a truncated preview would hide exactly what gets written.
func KnowledgeArticleApply(w io.Writer, v *client.KnowledgeArticleApplyResponse) {
	if v == nil {
		return
	}
	if !v.Changed {
		_, _ = fmt.Fprintf(w, "no change · %s article %s already matches\n", v.Provider, v.Article.ID)
		return
	}
	parts := []string{applyVerb(v) + " " + articleLabel(v)}
	if len(v.Changes) > 0 {
		parts = append(parts, "changes: "+strings.Join(v.Changes, ", "))
	}
	if !v.DryRun {
		if v.Article.DraftSaved {
			parts = append(parts, "draft saved (live untouched)")
		}
		if v.Article.URL != "" {
			parts = append(parts, v.Article.URL)
		}
		if v.Resync != nil && v.Resync.Queued {
			parts = append(parts, "resync queued")
		}
	}
	_, _ = fmt.Fprintln(w, strings.Join(parts, " · "))
	if !v.DryRun {
		return
	}
	for _, req := range v.Requests {
		_, _ = fmt.Fprintf(w, "%s %s\n", req.Method, req.URL)
		if body := prettyJSON(req.Body); body != "" {
			_, _ = fmt.Fprintln(w, body)
		}
	}
}

// applyVerb reads present tense on a dry run ("what this WOULD do") and past tense on a real write.
func applyVerb(v *client.KnowledgeArticleApplyResponse) string {
	if v.DryRun {
		return "dry-run · " + v.Op
	}
	switch v.Op {
	case "create":
		return "created"
	case "update":
		return "updated"
	default:
		return v.Op
	}
}

func articleLabel(v *client.KnowledgeArticleApplyResponse) string {
	label := fmt.Sprintf("%s article %s", v.Provider, v.Article.ID)
	if v.Article.Title != "" {
		label += fmt.Sprintf(" %q", v.Article.Title)
	}
	return label
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return buf.String()
}
