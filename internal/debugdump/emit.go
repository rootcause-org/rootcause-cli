package debugdump

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// EmitJSONL writes the drill-down event log: a {"type":"run"} header line (run metadata + full
// draft/note bodies + the untrimmed system prompt + egress) followed by one {"type":"event"} line per
// tool call, every field FULL and untruncated, keyed by `disp`. Header rollups are `run_`-prefixed so
// event-space jq queries (`select(.cost_usd > 0.01)`) never match the header.
func EmitJSONL(w io.Writer, full *client.FullResponse) error {
	events := decorate(full.Events)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	r := full.Run
	model := r.Model
	if model == "" {
		for _, e := range events {
			if e.src.Model != "" {
				model = e.src.Model
				break
			}
		}
	}
	header := map[string]any{
		"type":              "run",
		"run_id":            r.RunID,
		"project":           r.Project,
		"status":            r.Status,
		"kind":              r.Kind,
		"trigger":           emptyNil(r.Trigger),
		"brain_ref":         emptyNil(r.BrainRef),
		"error":             emptyNil(r.Error),
		"thread_id":         emptyNil(r.ThreadID),
		"session_id":        emptyNil(r.SessionID),
		"topic":             emptyNil(r.Topic),
		"question":          emptyNil(r.Question),
		"warm_start_digest": emptyNil(r.WarmStartDigest),
		"grounding_seed":    emptyNil(r.GroundingSeed),
		"system_prompt":     emptyNil(r.SystemPrompt),
		"created_at":        emptyNil(r.CreatedAt),
		"finished_at":       emptyNil(r.FinishedAt),
		"model":             emptyNil(model),
		"run_cost_usd":      r.RunCostUSD,
		"run_total_tokens":  r.RunTotalTokens,
		"draft":             emptyNil(r.Draft),
		"notes":             notesJSON(r.Notes),
		"metadata":          metadataJSON(r.Metadata),
		"egress":            egressJSON(r.Egress),
	}
	if err := enc.Encode(header); err != nil {
		return err
	}
	for _, e := range events {
		line := map[string]any{
			"type":         "event",
			"disp":         e.disp,
			"seq":          e.src.Seq,
			"grounding":    e.grounding,
			"tool":         e.src.Tool,
			"label":        e.label,
			"command":      e.command,
			"stdout":       emptyNil(e.src.Stdout),
			"stderr":       emptyNil(e.src.Stderr),
			"exit_code":    e.src.ExitCode,
			"status":       e.src.Status,
			"duration_ms":  e.src.DurationMs,
			"at":           emptyNil(e.src.At),
			"reasoning":    emptyNil(e.src.Reasoning),
			"cost_usd":     numOrNil(e.src.CostUSD),
			"total_tokens": int64OrNil(e.src.TotalTokens),
			"model":        emptyNil(e.src.Model),
		}
		// Bash's full input is `command`; other tools carry their structured input in `args`.
		if e.src.Tool != "bash" {
			if len(e.src.Args) > 0 {
				line["args"] = json.RawMessage(e.src.Args)
			} else {
				line["args"] = map[string]any{}
			}
		}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return nil
}

// RenderIndex builds the THIN markdown index: the run header, question, outcome gist, a main-loop
// timeline table (mechanical search/read steps omitted), auto-flagged anomalies, files read, an egress
// summary, and a Drill-down block of example jq calls. It is deliberately small — the JSONL is where the
// full detail lives; this file only says WHERE to look.
func RenderIndex(full *client.FullResponse) string {
	events := decorate(full.Events)
	r := full.Run
	jsonlName := JSONLName(full)

	var main, pre []decEvent
	for _, e := range events {
		if e.grounding {
			pre = append(pre, e)
		} else {
			main = append(main, e)
		}
	}
	model := r.Model
	if model == "" {
		for _, e := range events {
			if e.src.Model != "" {
				model = e.src.Model
				break
			}
		}
	}
	blocked := 0
	for _, g := range r.Egress {
		if g.Blocked {
			blocked += g.Count
		}
	}

	var L []string
	add := func(s ...string) { L = append(L, s...) }

	add(fmt.Sprintf("# Run %s — %s · %s · %s", short8(r.RunID), orQ(r.Project), r.Status, r.Kind), "")
	add(fmt.Sprintf("- **Run ID:** `%s`", r.RunID))
	if r.BrainRef != "" || r.Trigger == "test" {
		add(fmt.Sprintf("- **Test run** · brain_ref `%s` · trigger `%s` — side-effect-free",
			orMain(r.BrainRef), orQ(r.Trigger)))
	}
	if u := traceURL(r.Metadata); u != "" {
		add(fmt.Sprintf("- **Run page (human view):** %s", u))
	}
	add(fmt.Sprintf("- **Thread / Session:** `%s` / `%s`", r.ThreadID, r.SessionID))
	add(fmt.Sprintf("- **Created / Finished:** %s / %s", orQ(r.CreatedAt), orParen(r.FinishedAt, "unfinished")))
	add(fmt.Sprintf("- **Model:** `%s` · **Cost:** %s · **Tokens:** %d", orQ(model), orQ(cost(r.RunCostUSD)), r.RunTotalTokens))
	steps := fmt.Sprintf("- **Steps:** %d main", len(main))
	if len(pre) > 0 {
		steps += fmt.Sprintf(" + %d grounding", len(pre))
	}
	steps += fmt.Sprintf(" · **Egress:** %d", len(r.Egress))
	if blocked > 0 {
		steps += fmt.Sprintf(" (%d blocked)", blocked)
	}
	add(steps)
	add(fmt.Sprintf("- **Events (full, queryable):** `%s` — one JSON object per event; jq it (see Drill down).", jsonlName), "")

	add("## Question", "")
	if r.Question != "" {
		add(fence(r.Question, ""))
	} else {
		add("_(none captured)_")
	}
	add("", "## Outcome", "")
	add(renderOutcome(r)...)

	add("", "## Timeline — main-loop steps (search/read steps omitted — see Files the run read)", "")
	var rows []decEvent
	for _, e := range main {
		if e.label == "search files" || e.label == "read file" {
			continue
		}
		rows = append(rows, e)
	}
	if len(rows) > 0 {
		add("| # | label | code | exit | dur | output | reasoning gist |", "|---|---|---|---|---|---|---|")
		for _, e := range rows {
			failed := e.src.ExitCode != 0 || e.src.Status != "ok"
			outLimit := 100
			if failed {
				outLimit = 300
			}
			out := cell(firstNonEmpty(e.src.Stdout, e.src.Stderr), outLimit)
			outCell := ""
			if out != "" {
				outCell = "`" + out + "`"
			}
			add(fmt.Sprintf("| %s | %s | `%s` | %d | %s | %s | %s |",
				e.disp, e.label, cell(e.command, 100), e.src.ExitCode, dur(e.src.DurationMs), outCell, cell(e.gist, 90)))
		}
	} else {
		add("_(no main-loop tool calls recorded)_")
	}

	add("", "## Flags", "")
	fl := flags(full, events)
	if len(fl) == 0 {
		add("_(none)_")
	} else {
		for _, f := range fl {
			add("- " + f)
		}
	}

	if files := filesRead(events); len(files) > 0 {
		add("", "## Files the run read", "")
		for _, f := range files {
			add("- `" + f + "`")
		}
	}

	if len(r.Egress) > 0 {
		add("", "## Egress (by host)", "")
		type hk struct{ host, dec string }
		counts := map[hk]int{}
		for _, g := range r.Egress {
			dec := "allow"
			if g.Blocked {
				dec = "block"
			}
			counts[hk{g.Host, dec}] += g.Count
		}
		keys := make([]hk, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].host != keys[j].host {
				return keys[i].host < keys[j].host
			}
			return keys[i].dec < keys[j].dec
		})
		for _, k := range keys {
			add(fmt.Sprintf("- `%s` — %d× %s", k.host, counts[k], k.dec))
		}
	}

	add("", fmt.Sprintf("## Drill down — `%s`", jsonlName), "",
		"One JSON object per line: line 1 is the run header, the rest are events keyed by `disp` (the `#` "+
			"column above). Pull the FULL command/output/reasoning with jq:", "",
		fence(strings.Join([]string{
			fmt.Sprintf(`jq -r 'select(.disp=="23").command' %s   # full code of step 23`, jsonlName),
			fmt.Sprintf(`jq -r 'select(.disp=="23").stdout'  %s   # its output / traceback`, jsonlName),
			fmt.Sprintf(`jq -r 'select(.disp=="23").stdout | .[0:2000]' %s   # windowed when flagged large`, jsonlName),
			fmt.Sprintf(`jq -r 'select(.exit_code != null and .exit_code != 0).disp' %s   # failed steps`, jsonlName),
			fmt.Sprintf(`jq -r 'select(.command // "" | contains("invoice")).disp' %s   # steps touching X`, jsonlName),
			fmt.Sprintf(`jq -r 'select(.reasoning) | .disp + " " + .reasoning' %s   # reasoning per step`, jsonlName),
		}, "\n"), "sh"))
	add("")
	return strings.Join(L, "\n")
}

// renderOutcome shows the draft gist (first 8 lines) + note gists, or a "no callback" marker.
func renderOutcome(r client.RunHeader) []string {
	if r.Draft == "" && len(r.Notes) == 0 && len(r.Metadata) == 0 {
		return []string{"_(no stored callback — run errored or never produced one)_"}
	}
	var out []string
	if r.Draft != "" {
		lines := strings.Split(strings.TrimSpace(r.Draft), "\n")
		g := strings.Join(lines, "\n")
		if len(lines) > 8 {
			g = strings.Join(lines[:8], "\n") + fmt.Sprintf("\n… (%d lines total — full text in the .jsonl run header)", len(lines))
		}
		out = append(out, fmt.Sprintf("**Draft** (%d lines):", len(lines)), "", fence(g, ""), "")
	} else {
		out = append(out, "**Draft:** none", "")
	}
	for _, n := range r.Notes {
		key := ""
		if n.Key != "" {
			key = " `" + n.Key + "`"
		}
		out = append(out, "**Note"+key+":**", "", fence(gist(n.Body, 400), ""), "")
	}
	return out
}

// --- flags (attention-directing anomalies) -----------------------------------------------------------

var grepRx = regexp.MustCompile(`^\s*(rg|grep|egrep|fgrep)\b`)

// flags surfaces where "why did it do that" questions are likely to live: errors, failed steps, blocked
// egress, repeated commands, cost spikes, large output. A trimmed port of the shared renderer's flags().
func flags(full *client.FullResponse, events []decEvent) []string {
	r := full.Run
	var out []string
	if r.Error != "" {
		out = append(out, fmt.Sprintf("run errored: `%s`", r.Error))
	}
	if r.Draft == "" && len(r.Notes) == 0 && len(r.Metadata) == 0 {
		out = append(out, "no stored callback — the run never produced one")
	}
	for _, e := range events {
		if benignGrepMiss(e) {
			// grep exit 1 = no match, not a failure
		} else if e.src.Status != "ok" {
			s := fmt.Sprintf("[%s] %s", e.disp, e.src.Status)
			if e.src.ExitCode != 0 {
				s += fmt.Sprintf(" (exit %d)", e.src.ExitCode)
			}
			out = append(out, s)
		} else if e.src.ExitCode != 0 {
			out = append(out, fmt.Sprintf("[%s] exit %d", e.disp, e.src.ExitCode))
		}
		if strings.Contains(e.src.Stdout+e.src.Stderr, "EGRESS_BLOCKED") {
			out = append(out, fmt.Sprintf("[%s] output mentions EGRESS_BLOCKED", e.disp))
		}
		if len(e.src.Stdout) > 20_000 {
			out = append(out, fmt.Sprintf("[%s] large stdout (%d KB)", e.disp, len(e.src.Stdout)/1024))
		}
	}

	// Repeated identical bash commands — possible flailing.
	seen := map[string][]string{}
	for _, e := range events {
		if e.src.Tool == "bash" && strings.TrimSpace(e.command) != "" {
			k := strings.Join(strings.Fields(e.command), " ")
			seen[k] = append(seen[k], e.disp)
		}
	}
	repeats := make([]string, 0, len(seen))
	for k := range seen {
		repeats = append(repeats, k)
	}
	sort.Strings(repeats)
	for _, k := range repeats {
		if steps := seen[k]; len(steps) > 1 {
			out = append(out, fmt.Sprintf("[%s] identical command ran %d×: `%s`", strings.Join(steps, ", "), len(steps), cell(k, 60)))
		}
	}

	// Cost spikes: a turn well above the median paid turn.
	var costs []float64
	for _, e := range events {
		if e.src.CostUSD > 0 {
			costs = append(costs, e.src.CostUSD)
		}
	}
	if len(costs) >= 4 {
		med := median(costs)
		for _, e := range events {
			c := e.src.CostUSD
			if c > 0 && med > 0 && c > 4*med && c > 0.01 {
				out = append(out, fmt.Sprintf("[%s] cost spike %s (%.0f× median turn)", e.disp, cost(c), c/med))
			}
		}
	}

	for _, g := range r.Egress {
		if g.Blocked {
			out = append(out, fmt.Sprintf("egress BLOCKED: `%s` (%d×)", g.Host, g.Count))
		}
	}
	return out
}

func benignGrepMiss(e decEvent) bool {
	return e.src.ExitCode == 1 && (e.src.Status == "ok" || e.src.Status == "error") &&
		strings.TrimSpace(e.src.Stderr) == "" &&
		grepRx.MatchString(cdPrefix.ReplaceAllString(e.command, ""))
}

// pathRx matches /brain, /mirrors, /kb paths in commands — the bridge to "what did the run read". The
// leading (^|[^\w-]) group emulates the reference renderer's negative lookbehind (RE2 has none): a path
// must not be glued onto a preceding word/hyphen char, so `foo/brain/x.py` isn't mis-read as a path.
// Group 1 is the boundary (discarded); group 2 is the path.
var pathRx = regexp.MustCompile(`(^|[^\w-])(/(?:brain|mirrors|kb)/[A-Za-z0-9._/@%+-]*[A-Za-z0-9_])`)

// filesRead returns the sorted FILE paths (those with an extension) the run's bash commands touched.
func filesRead(events []decEvent) []string {
	set := map[string]struct{}{}
	for _, e := range events {
		if e.src.Tool != "bash" {
			continue
		}
		for _, m := range pathRx.FindAllStringSubmatch(e.command, -1) {
			p := m[2] // the path (group 1 is the leading boundary char)
			last := p[strings.LastIndex(p, "/")+1:]
			if strings.Contains(last, ".") { // a dot in the basename ⇒ a file, not a dir
				set[p] = struct{}{}
			}
		}
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files
}

// --- small JSON/format helpers -----------------------------------------------------------------------

func emptyNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func numOrNil(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

func int64OrNil(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func notesJSON(notes []client.Note) []map[string]any {
	out := make([]map[string]any, 0, len(notes))
	for _, n := range notes {
		out = append(out, map[string]any{"key": n.Key, "body": n.Body})
	}
	return out
}

func metadataJSON(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	return m
}

func egressJSON(egress []client.EgressItem) []map[string]any {
	out := make([]map[string]any, 0, len(egress))
	for _, g := range egress {
		out = append(out, map[string]any{"host": g.Host, "count": g.Count, "blocked": g.Blocked})
	}
	return out
}

func traceURL(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	for _, k := range []string{"run_url", "trace_url"} {
		if v, ok := meta[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func orMain(s string) string {
	if s == "" {
		return "(main)"
	}
	return s
}

func orParen(s, fallback string) string {
	if s == "" {
		return "(" + fallback + ")"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func median(vals []float64) float64 {
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
