// The `rc ask` scenario views (email / raw / dry-scope) plus the shared draft/note/action blocks.
// The renderers are pure functions of the wire structs (no I/O beyond the passed writer, no clock) so
// golden tests pin them exactly. Timestamps are shown as the server sent them, keeping goldens stable.

package render

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// AskEmail renders `rc ask --scenario email`: the reviewable support outcome first (draft, notes,
// actions/PR), with enough run metadata to jump into the trace.
func AskEmail(w io.Writer, r *client.RunDetail, full *client.FullResponse) {
	renderAskHeader(w, r, "email")
	if why := askDecline(r, full); why != "" {
		_, _ = fmt.Fprintf(w, "\nDecline reason:\n%s\n", why)
	}
	if draft := askDraft(r, full); draft != "" {
		_, _ = fmt.Fprintf(w, "\nDraft:\n%s\n", draft)
	}
	for _, n := range askNotes(r, full) {
		body := noteBody(n)
		if body == "" {
			continue
		}
		label := n.Key
		if label == "" {
			label = "note"
		}
		_, _ = fmt.Fprintf(w, "\nNote (%s):\n%s\n", label, body)
		if len(n.Actions) > 0 {
			renderNoteActions(w, n.Actions)
		}
	}
	renderProposedActions(w, askActions(r, full))
	renderSourcePR(w, askSourcePR(r, full))
	renderMetadata(w, askMetadata(r, full))
}

// AskRaw renders `rc ask --scenario raw`: one direct Markdown answer plus machine/action affordances.
func AskRaw(w io.Writer, r *client.RunDetail) {
	renderAskHeader(w, r, "raw")
	answer := strings.TrimSpace(r.AnswerMarkdown)
	if answer == "" {
		answer = strings.TrimSpace(r.DraftMarkdown)
	}
	if answer != "" {
		_, _ = fmt.Fprintf(w, "\nAnswer:\n%s\n", answer)
	}
	renderProposedActions(w, r.ProposedActions)
	renderSourcePR(w, r.SourcePR)
	renderMetadata(w, r.Metadata)
}

// AskDryScope renders `rc ask --dry-scope`: the principal scope this run WOULD get, resolved without
// running the agent. On a fail-closed refusal it prints the reason (no record); otherwise the resolved
// principalScopeRecord — resolution first (the finding-relevant field), then the identity, claims, and
// table grants. IDs are shown UNMASKED: this is an operator/red-team tool, not customer-facing output.
func AskDryScope(w io.Writer, p *client.ScopePreview) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Dry-scope:\t%t\n", p.Resolved)
	if !p.Resolved {
		reason := p.Error
		if reason == "" {
			reason = "refused (no reason given)"
		}
		_, _ = fmt.Fprintf(tw, "Refused:\t%s\n", reason)
		_ = tw.Flush()
		return
	}
	s := p.Scope
	if s == nil {
		_, _ = fmt.Fprintf(tw, "Scope:\t(none)\n")
		_ = tw.Flush()
		return
	}
	_, _ = fmt.Fprintf(tw, "Resolution:\t%s\n", s.Resolution)
	if s.Kind != "" {
		_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", s.Kind)
	}
	if s.ExternalID != "" {
		_, _ = fmt.Fprintf(tw, "External ID:\t%s\n", s.ExternalID)
	}
	if s.AssertedBy != "" {
		_, _ = fmt.Fprintf(tw, "Asserted by:\t%s\n", s.AssertedBy)
	}
	if s.Assurance != "" {
		_, _ = fmt.Fprintf(tw, "Assurance:\t%s\n", s.Assurance)
	}
	for _, name := range sortedClaimKeys(s.Claims) {
		_, _ = fmt.Fprintf(tw, "Claim %s:\t%v\n", name, s.Claims[name])
	}
	if g := s.TableGrants; g != nil {
		grant := strings.Join(g.Granted, ", ")
		if g.All {
			grant = "all tables"
		}
		if grant == "" {
			grant = "(none)"
		}
		_, _ = fmt.Fprintf(tw, "Table grants:\t%s\n", grant)
	}
	_ = tw.Flush()
}

// sortedClaimKeys returns a claims map's keys in stable order so a dump renders deterministically (golden-safe).
func sortedClaimKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func renderAskHeader(w io.Writer, r *client.RunDetail, scenario string) {
	if r.Scenario != "" {
		scenario = r.Scenario
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	_, _ = fmt.Fprintf(tw, "Scenario:\t%s\n", scenario)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	if r.Category != "" {
		_, _ = fmt.Fprintf(tw, "Category:\t%s\n", r.Category)
	}
	if r.Outcome != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", r.Outcome)
	} else if oc := metaString(r.Metadata, "outcome"); oc != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", oc)
	}
	_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if d := runDetailDuration(r); d != "" {
		_, _ = fmt.Fprintf(tw, "Duration:\t%s\n", d)
	}
	if r.Turns > 0 {
		_, _ = fmt.Fprintf(tw, "Turns:\t%d\n", r.Turns)
	}
	if r.BashTotal > 0 {
		_, _ = fmt.Fprintf(tw, "Bash:\t%d\n", r.BashTotal)
	}
	if r.RunURL != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", r.RunURL)
	} else if u := metaString(r.Metadata, "run_url"); u != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", u)
	}
	_ = tw.Flush()
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
}

func askDraft(r *client.RunDetail, full *client.FullResponse) string {
	if full != nil {
		for _, s := range []string{full.Run.Draft, full.Run.DraftMarkdown} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	for _, s := range []string{r.DraftMarkdown, r.AnswerMarkdown} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func askDecline(r *client.RunDetail, full *client.FullResponse) string {
	if full != nil {
		for _, s := range []string{full.Run.Decline, full.Run.DeclineReason} {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	if strings.TrimSpace(r.DeclineReason) != "" {
		return r.DeclineReason
	}
	if r.Debug != nil {
		return r.Debug.DeclineReason
	}
	return ""
}

func askNotes(r *client.RunDetail, full *client.FullResponse) []client.Note {
	if full != nil && len(full.Run.Notes) > 0 {
		return full.Run.Notes
	}
	return r.Notes
}

func askActions(r *client.RunDetail, full *client.FullResponse) []client.ProposedAction {
	if len(r.ProposedActions) > 0 {
		return r.ProposedActions
	}
	if full != nil {
		return full.Run.ProposedActions
	}
	return nil
}

func askSourcePR(r *client.RunDetail, full *client.FullResponse) *client.SourcePR {
	if r.SourcePR != nil {
		return r.SourcePR
	}
	if full != nil {
		return full.Run.SourcePR
	}
	return nil
}

func askMetadata(r *client.RunDetail, full *client.FullResponse) map[string]any {
	if full == nil || len(full.Run.Metadata) == 0 {
		return r.Metadata
	}
	if len(r.Metadata) == 0 {
		return full.Run.Metadata
	}
	out := make(map[string]any, len(full.Run.Metadata)+len(r.Metadata))
	for k, v := range full.Run.Metadata {
		out[k] = v
	}
	for k, v := range r.Metadata {
		out[k] = v
	}
	return out
}

func noteBody(n client.Note) string {
	for _, s := range []string{n.Body, n.BodyMarkdown, n.BodyText, n.BodyHTML} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func renderNoteActions(w io.Writer, actions []client.NoteAction) {
	if len(actions) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nNote actions (%d):\n", len(actions))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tLABEL")
	for _, a := range actions {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", a.ID, a.Label)
	}
	_ = tw.Flush()
	for _, a := range actions {
		if a.URL != "" {
			_, _ = fmt.Fprintf(w, "    url: %s\n", a.URL)
		}
		if a.Description != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", a.Description)
		}
	}
}

func renderProposedActions(w io.Writer, actions []client.ProposedAction) {
	if len(actions) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\nActions (%d):\n", len(actions))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ID\tACTION\tLABEL")
	for _, a := range actions {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", a.ID, a.Slug, a.Label)
	}
	_ = tw.Flush()
	for _, a := range actions {
		if a.URL != "" {
			_, _ = fmt.Fprintf(w, "    url: %s\n", a.URL)
		}
		if a.Description != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", a.Description)
		}
	}
}

func renderSourcePR(w io.Writer, pr *client.SourcePR) {
	if pr == nil {
		return
	}
	_, _ = fmt.Fprintln(w, "\nPR:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if pr.Title != "" {
		_, _ = fmt.Fprintf(tw, "  Title:\t%s\n", pr.Title)
	}
	if pr.Repo != "" {
		_, _ = fmt.Fprintf(tw, "  Repo:\t%s\n", pr.Repo)
	}
	if pr.URL != "" {
		_, _ = fmt.Fprintf(tw, "  URL:\t%s\n", pr.URL)
	}
	_ = tw.Flush()
	if strings.TrimSpace(pr.Body) != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", pr.Body)
	}
}

func renderMetadata(w io.Writer, md map[string]any) {
	keys := sortedMetadataKeys(md)
	if len(keys) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nMetadata:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, k := range keys {
		_, _ = fmt.Fprintf(tw, "  %s:\t%v\n", k, md[k])
	}
	_ = tw.Flush()
}

// suppressedMetadataKeys never reach a rendered surface: outcome/run_url already have dedicated rows.
// The spend/token and serving-model keys are barred product-wide and live in client.HostOnlyMetadataKey,
// shared with the other passthrough (debugdump's JSONL run header).
var suppressedMetadataKeys = map[string]bool{"outcome": true, "run_url": true}

func sortedMetadataKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		switch {
		case suppressedMetadataKeys[k] || client.HostOnlyMetadataKey(k):
			continue
		default:
			keys = append(keys, k)
		}
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
