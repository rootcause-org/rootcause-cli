package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Guards is the red-team readback of every security checkpoint for one run: a compact table, one row
// per checkpoint in evaluation order (injection_scan, principal_scope, egress, output_guard,
// output_judge, final). Blocks, violations, and fail-open (shipped unjudged) are surfaced loudly; a
// checkpoint that did not apply says so explicitly rather than reading as a clean pass.
func Guards(w io.Writer, g *client.GuardsView) {
	if g == nil {
		_, _ = fmt.Fprintln(w, "(no guards on this run — needs a project-admin token and a server that exposes them)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CHECKPOINT\tVERDICT\tDETAIL")
	rows := []struct {
		name            string
		verdict, detail string
	}{}
	add := func(name, verdict, detail string) {
		rows = append(rows, struct {
			name            string
			verdict, detail string
		}{name, verdict, detail})
	}
	iv, id := guardsInjection(g.InjectionScan)
	add("injection_scan", iv, id)
	pv, pd := guardsPrincipal(g.PrincipalScope)
	add("principal_scope", pv, pd)
	ev, ed := guardsEgress(g.Egress)
	add("egress", ev, ed)
	ogv, ogd := guardsOutputGuard(g.OutputGuard)
	add("output_guard", ogv, ogd)
	ojv, ojd := guardsOutputJudge(g.OutputJudge)
	add("output_judge", ojv, ojd)
	fv, fd := guardsFinal(g.Final)
	add("final", fv, fd)
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.name, r.verdict, blank(r.detail))
	}
	_ = tw.Flush()
}

func guardsInjection(s *client.GuardsInjection) (verdict, detail string) {
	if s == nil {
		return "n/a", "non-channel run"
	}
	if !s.Ran {
		return "did not run", ""
	}
	detail = joinNonEmpty(" · ", guardCat(s.Category), guardConf(s.Confidence), s.Rationale)
	if s.Blocked {
		return "BLOCKED", detail
	}
	return "clean", detail
}

func guardsPrincipal(raw json.RawMessage) (verdict, detail string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "none", "run carried no principal scope"
	}
	var ps struct {
		Kind       string `json:"kind"`
		Resolution string `json:"resolution"`
		Assurance  string `json:"assurance"`
	}
	if err := json.Unmarshal(raw, &ps); err != nil {
		return "present", "unparseable principal_scope"
	}
	verdict = blank(ps.Resolution)
	detail = joinNonEmpty(" · ", kv("kind", ps.Kind), kv("assurance", ps.Assurance))
	return verdict, detail
}

func guardsEgress(s *client.GuardsEgress) (verdict, detail string) {
	if s == nil {
		return "n/a", "no egress reported"
	}
	verdict = fmt.Sprintf("%d allowed / %d blocked", s.Allowed, s.Blocked)
	if s.Blocked > 0 {
		verdict = "BLOCKED " + verdict
	}
	parts := make([]string, 0, len(s.Hosts))
	for _, h := range s.Hosts {
		parts = append(parts, fmt.Sprintf("%s %s×%d", h.Host, h.Decision, h.Count))
	}
	return verdict, strings.Join(parts, ", ")
}

func guardsOutputGuard(s *client.GuardsOutputGuard) (verdict, detail string) {
	if s == nil {
		return "not evaluated", "non-chat run"
	}
	if !s.Evaluated {
		return "not evaluated", "guard did not run"
	}
	detail = joinNonEmpty(" · ",
		kv("surface_guarded", fmt.Sprintf("%t", s.SurfaceGuarded)),
		listDetail("rules", s.Rules),
		fmt.Sprintf("source_hits=%d", s.SourceHits),
		fmt.Sprintf("source_score=%d", s.SourceScore),
	)
	if s.Violated {
		return "VIOLATED", detail
	}
	return "clean", detail
}

func guardsOutputJudge(s *client.GuardsOutputJudge) (verdict, detail string) {
	if s == nil {
		return "did not run", ""
	}
	if s.Error != "" {
		return "FAIL-OPEN", "shipped unjudged (" + s.Error + ")"
	}
	detail = joinNonEmpty(" · ", guardCat(s.Category), guardConf(s.Confidence), listDetail("reasons", s.Reasons))
	switch {
	case s.Blocked:
		return "BLOCKED", detail
	case s.Violation:
		return "VIOLATION", detail
	case s.Fired:
		return "fired (allowed)", detail
	default:
		return "clean", detail
	}
}

func guardsFinal(s *client.GuardsFinal) (verdict, detail string) {
	if s == nil {
		return "n/a", ""
	}
	detail = joinNonEmpty(" · ",
		kv("draft", boolMark(s.HasDraft)),
		kv("note", boolMark(s.HasNote)),
		kv("guardrail", s.Guardrail),
		s.DeclineReason,
	)
	return blank(s.Outcome), detail
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func kv(key, val string) string {
	if val == "" {
		return ""
	}
	return key + "=" + val
}

func guardCat(cat string) string {
	if cat == "" {
		return ""
	}
	return "category=" + cat
}

func guardConf(c float64) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("confidence=%.2f", c)
}

func listDetail(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	return label + "=[" + strings.Join(items, ",") + "]"
}

func boolMark(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
