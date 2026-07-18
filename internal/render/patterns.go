// This file is the FAT side of `rc fleet patterns`: it ports run_patterns.py's clustering over the THIN
// /run-events + /egress-log feeds — the bash-failure signatures, the recurring stderr/error themes,
// and the blocked-egress host clusters, each ending in a `suggested fix:` stub for the reviewing LLM.
// The server ships raw rows; ALL masking/grouping/ranking happens here (the doctrine). The command layer
// pages the two feeds; this file turns the rows into ranked markdown an LLM can read whole.
//
// Two of run_patterns.py's four sections (run-error themes from runs.error, repeated questions from
// runs.question) read run BODIES the thin API deliberately does NOT expose (privacy — the index is
// category-only). We reconstruct the "recurring error theme" signal from bash stderr signatures instead
// (the same _mask collapse), which is what actually drives most failure patterns; the question-runbook
// section is dropped (no input) and noted in the command help.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// PatternsOptions carries the window scope + caps (mirrors run_patterns.py's args).
type PatternsOptions struct {
	Days int
	Top  int // max patterns per section
	Kind string
}

// --- masking (ported from run_patterns.py _MASKS) ---

var (
	maskUUID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	maskStr  = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'`)
	// No leading-letter digit: "600s"/"orders_2024" mask too (the unit/suffix stays, the digits go).
	maskNum = regexp.MustCompile(`(^|[^A-Za-z])\d[\d_.,:]*`)
	wsRun   = regexp.MustCompile(`\s+`)
)

// mask collapses the variable parts of a string so twin errors (2024 vs 2025, uuid A vs B) share one
// signature. Order matters: uuids before quoted strings before bare numbers (run_patterns.py order).
func mask(s string) string {
	s = maskUUID.ReplaceAllString(s, "<uuid>")
	s = maskStr.ReplaceAllString(s, "<str>")
	s = maskNum.ReplaceAllStringFunc(s, func(m string) string {
		// Preserve a leading non-digit boundary char the regex captured.
		if m != "" && (m[0] < '0' || m[0] > '9') {
			return string(m[0]) + "<n>"
		}
		return "<n>"
	})
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// firstLine returns the first non-empty line of text (run_patterns.py _first_line).
func patternsFirstLine(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// --- command classifier (ported from rootcause-runtime _render.py _label / _code_gist) ---

var cdPrefix = regexp.MustCompile(`^\s*cd\s+\S+\s*&&\s*`)

type labelRule struct {
	rx    *regexp.Regexp
	label string
}

var labelRules = []labelRule{
	{regexp.MustCompile(`actions/[A-Za-z0-9._\-]+/preflight\.(?:py|sh|rb)\b`), "check action"},
	{regexp.MustCompile(`(?i)\blib\.db\b|\bpsql\b|\bSELECT\b`), "db query"},
	{regexp.MustCompile(`(?i)\bstripe\b`), "stripe"},
	{regexp.MustCompile(`(?i)\bcloudwatch\b|\baws logs\b|\blib\.logs\b`), "cloudwatch"},
	{regexp.MustCompile(`\bcurl\b|\blib\.http\b|\bwget\b|\brequests\b`), "http"},
	{regexp.MustCompile(`^\s*(cat|head|tail|less|sed -n)\b`), "read file"},
	{regexp.MustCompile(`^\s*(rg|grep|find|ls|tree|fd)\b`), "search files"},
	{regexp.MustCompile(`\bpython3?\b|<<\s*'?EOF`), "python"},
}

// label classifies a bash command into a coarse intent (run_patterns.py imports _label). A leading
// `cd X &&` is stripped first so `cd /mirrors/x && rg …` reads as an rg, not a cd.
func label(cmd string) string {
	cmd = cdPrefix.ReplaceAllString(cmd, "")
	for _, r := range labelRules {
		if r.rx.MatchString(cmd) {
			return r.label
		}
	}
	for _, ln := range strings.Split(cmd, "\n") {
		words := strings.Fields(strings.TrimSpace(ln))
		if len(words) > 0 && !strings.HasPrefix(words[0], "#") {
			return clipStr(words[0], 20)
		}
	}
	return "bash"
}

var (
	pyUnwrapCD = cdPrefix
	pyUnwrapPy = regexp.MustCompile(`^\s*(?:uv run\s+)?python3?\s+-c\s+["']`)
	pySkipLine = regexp.MustCompile(`^\s*(import\s|from\s+\S+\s+import\s|sys\.path\.insert)`)
	trailQuote = regexp.MustCompile(`["']\s*$`)
)

// codeGist returns the first chars of the code that matters: strip the cd/python -c wrapper, the
// import/sys.path boilerplate, and a trailing closing quote (run_patterns.py imports _code_gist).
func codeGist(cmd string, limit int) string {
	cmd = pyUnwrapCD.ReplaceAllString(cmd, "")
	cmd = pyUnwrapPy.ReplaceAllString(cmd, "")
	cmd = trailQuote.ReplaceAllString(cmd, "")
	var kept []string
	for _, ln := range strings.Split(cmd, "\n") {
		t := strings.TrimSpace(ln)
		if t != "" && !pySkipLine.MatchString(ln) {
			kept = append(kept, t)
		}
	}
	return clipStr(strings.Join(kept, "; "), limit)
}

// --- clustering ---

// bashCluster is one bash-failure signature: (command label, exit, masked first stderr line), ranked by
// (distinct runs, count) — cross-run reach beats raw repetition (run_patterns.py cluster_bash).
type bashCluster struct {
	label    string
	exitCode int32
	sig      string
	count    int
	runs     map[string]bool
	cmd      string
	examples []string
	evidence []string
}

// commandOf pulls the bash command out of an event's raw args (args.command). "" when absent/non-bash.
func commandOf(e client.RunEvent) string {
	if len(e.Args) == 0 {
		return ""
	}
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(e.Args, &a)
	return a.Command
}

// clusterBash groups failing bash events (exit≠0 OR status≠ok) by signature.
func clusterBash(events []client.RunEvent) []*bashCluster {
	groups := map[string]*bashCluster{}
	var order []string
	for _, e := range events {
		if e.Tool != "bash" {
			continue
		}
		if e.ExitCode == 0 && e.Status == "ok" {
			continue
		}
		cmd := commandOf(e)
		sig := mask(patternsFirstLine(e.Stderr))
		key := label(cmd) + "\x00" + fmt.Sprint(e.ExitCode) + "\x00" + sig
		g := groups[key]
		if g == nil {
			g = &bashCluster{
				label: label(cmd), exitCode: e.ExitCode, sig: sig,
				runs: map[string]bool{}, cmd: codeGist(cmd, 100), evidence: excerpt(e),
			}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		g.runs[e.RunID] = true
		if len(g.examples) < 2 {
			g.examples = append(g.examples, fmt.Sprintf("%s[%d]", e.RunID, e.Seq))
		}
	}
	out := make([]*bashCluster, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].runs) != len(out[j].runs) {
			return len(out[i].runs) > len(out[j].runs)
		}
		return out[i].count > out[j].count
	})
	return out
}

// excerpt returns up to 3 non-empty evidence lines (stderr preferred, else stdout) clipped to 120 chars
// (run_patterns.py _excerpt).
func excerpt(e client.RunEvent) []string {
	src := e.Stderr
	if src == "" {
		src = e.Stdout
	}
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if len(t) > 120 {
			t = t[:119] + "…"
		}
		out = append(out, t)
		if len(out) == 3 {
			break
		}
	}
	return out
}

// egressCluster is one (host) blocked-egress signature, ranked by count (run_patterns.py cluster_egress,
// minus the project axis — `rc fleet patterns` is already one project).
type egressCluster struct {
	host  string
	count int
	runs  map[string]bool
}

func clusterEgress(rows []client.EgressRow) []*egressCluster {
	groups := map[string]*egressCluster{}
	var order []string
	for _, r := range rows {
		if r.Decision != "block" {
			continue
		}
		g := groups[r.Host]
		if g == nil {
			g = &egressCluster{host: r.Host, runs: map[string]bool{}}
			groups[r.Host] = g
			order = append(order, r.Host)
		}
		g.count++
		g.runs[r.RunID] = true
	}
	out := make([]*egressCluster, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].count > out[j].count })
	return out
}

// --- render ---

// Patterns renders the clustered failure report (markdown, like run_patterns.py). failingBash is the
// COUNT of failing bash events scanned (for the header); the caller computes it.
func Patterns(w io.Writer, events []client.RunEvent, egress []client.EgressRow, httpRows []client.HTTPAuditRow, opt PatternsOptions) {
	if opt.Top <= 0 {
		opt.Top = 15
	}
	bash := clusterBash(events)
	egr := clusterEgress(egress)
	allowedRows := make([]client.HTTPAuditRow, 0, len(httpRows))
	for _, row := range httpRows {
		if row.Decision != "block" {
			allowedRows = append(allowedRows, row)
		}
	}
	endpoints := rollupHTTP(allowedRows)
	abnormalWrites := abnormalWriteRollups(endpoints)

	failing := 0
	for _, e := range events {
		if e.Tool == "bash" && (e.ExitCode != 0 || e.Status != "ok") {
			failing++
		}
	}
	blocked := 0
	for _, r := range egress {
		if r.Decision == "block" {
			blocked++
		}
	}

	_, _ = fmt.Fprintf(w, "# Run patterns — last %d days\n\n", opt.Days)
	_, _ = fmt.Fprintf(w, "%d failing bash events · %d blocked egress rows · %d HTTP attempts · %d events scanned\n", failing, blocked, len(httpRows), len(events))
	_, _ = fmt.Fprintln(w, "Rank by: cross-run reach · frequency. Failure and anomaly patterns end in a suggested-fix stub.")
	_, _ = fmt.Fprintln(w)

	anything := false

	if len(bash) > 0 {
		anything = true
		_, _ = fmt.Fprintln(w, "## Bash failure clusters")
		_, _ = fmt.Fprintln(w)
		for i, g := range clipBash(bash, opt.Top) {
			_, _ = fmt.Fprintf(w, "### B%d. bash: %s · exit %d — %d× across %d run(s)\n", i+1, g.label, g.exitCode, g.count, len(g.runs))
			sig := g.sig
			if sig == "" {
				sig = "(no stderr)"
			}
			_, _ = fmt.Fprintf(w, "- sig: `%s`\n", sig)
			if g.cmd != "" {
				_, _ = fmt.Fprintf(w, "- cmd: `%s`\n", g.cmd)
			}
			if len(g.examples) > 0 {
				_, _ = fmt.Fprintf(w, "- examples: %s\n", strings.Join(backtickEach(g.examples), " · "))
			}
			for _, ln := range g.evidence {
				_, _ = fmt.Fprintf(w, "    %s\n", ln)
			}
			_, _ = fmt.Fprintln(w, "- suggested fix:")
			_, _ = fmt.Fprintln(w)
		}
	}

	if len(egr) > 0 {
		anything = true
		_, _ = fmt.Fprintln(w, "## Blocked egress")
		_, _ = fmt.Fprintln(w)
		for i, g := range clipEgress(egr, opt.Top) {
			_, _ = fmt.Fprintf(w, "### E%d. egress: `%s` — blocked %d× across %d run(s)\n", i+1, g.host, g.count, len(g.runs))
			_, _ = fmt.Fprintf(w, "- example runs: %s\n", strings.Join(backtickEach(sortedRunIDs(g.runs, 2)), " · "))
			_, _ = fmt.Fprintln(w, "- suggested fix:")
			_, _ = fmt.Fprintln(w)
		}
	}

	if len(endpoints) > 0 {
		anything = true
		_, _ = fmt.Fprintln(w, "## Allowed endpoint clusters")
		_, _ = fmt.Fprintln(w)
		for i, group := range clipEndpoints(endpoints, opt.Top) {
			_, _ = fmt.Fprintf(w, "### H%d. %s %s%s — %d× across %d run(s)\n",
				i+1, group.method, hostPrefix(group.host), group.endpoint, group.count, len(group.runs))
			_, _ = fmt.Fprintf(w, "- source: `%s` · errors: %d · retries: %d\n\n", group.source, group.errors, group.retries)
		}
	}

	if len(abnormalWrites) > 0 {
		anything = true
		_, _ = fmt.Fprintln(w, "## Abnormal write volume")
		_, _ = fmt.Fprintln(w)
		for i, group := range clipEndpoints(abnormalWrites, opt.Top) {
			_, _ = fmt.Fprintf(w, "### W%d. %s %s%s — %d× across %d run(s)\n",
				i+1, group.method, hostPrefix(group.host), group.endpoint, group.count, len(group.runs))
			_, _ = fmt.Fprintf(w, "- signal: %s\n", writeVolumeSignal(group))
			_, _ = fmt.Fprintln(w, "- suggested fix:")
			_, _ = fmt.Fprintln(w)
		}
	}

	truncated := (len(bash) - min(len(bash), opt.Top)) +
		(len(egr) - min(len(egr), opt.Top)) +
		(len(endpoints) - min(len(endpoints), opt.Top)) +
		(len(abnormalWrites) - min(len(abnormalWrites), opt.Top))
	if truncated > 0 {
		_, _ = fmt.Fprintf(w, "_(%d lower-ranked pattern(s) dropped — raise --top/--days)_\n", truncated)
	}
	if !anything {
		_, _ = fmt.Fprintln(w, "_(no failure patterns in window — a clean fleet)_")
	}
}

func abnormalWriteRollups(groups []*endpointRollup) []*endpointRollup {
	var out []*endpointRollup
	for _, group := range groups {
		switch strings.ToUpper(group.method) {
		case "GET", "HEAD", "OPTIONS":
			continue
		}
		runCount := max(1, len(group.runs))
		if group.count >= 10 || group.count > runCount*3 || group.retries > 0 {
			out = append(out, group)
		}
	}
	return out
}

func writeVolumeSignal(group *endpointRollup) string {
	runCount := max(1, len(group.runs))
	parts := []string{fmt.Sprintf("%.1f attempts/run", float64(group.count)/float64(runCount))}
	if group.retries > 0 {
		parts = append(parts, fmt.Sprintf("%d retries", group.retries))
	}
	if group.errors > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", group.errors))
	}
	return strings.Join(parts, " · ")
}

func hostPrefix(host string) string {
	if host == "" {
		return ""
	}
	return host
}

func clipEndpoints(s []*endpointRollup, n int) []*endpointRollup {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clipBash(s []*bashCluster, n int) []*bashCluster {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func clipEgress(s []*egressCluster, n int) []*egressCluster {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func backtickEach(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = "`" + v + "`"
	}
	return out
}

func sortedRunIDs(runs map[string]bool, n int) []string {
	ids := make([]string, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids
}

func clipStr(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " "))
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}
