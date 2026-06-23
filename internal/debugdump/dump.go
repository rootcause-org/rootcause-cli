// Package debugdump ports rootcause's rc-agent-debug decomposer to the CLI: it turns one run's /full
// bundle into TWO local files built for progressive disclosure — a JSONL event log (the drill-down
// target) and a THIN markdown index (where to look). The calling agent reads the index, then jqs the
// JSONL; the CLI never pre-summarizes a whole run into an LLM's context.
//
// The JSONL contract is the load-bearing seam, kept compatible with the Python operator script
// (.agents/skills/support/scripts/rc_agent_debug.py via the shared rootcause-runtime renderer): line 1
// is a {"type":"run"…} header, every later line is a {"type":"event"…} keyed by `disp` (the index's `#`
// column; grounding pre-steps are P1,P2,…, the main loop 1,2,…). Existing jq recipes
// (`select(.disp=="23").command`) keep working.
package debugdump

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// decEvent is one decorated event: the raw item plus the display fields the index + JSONL derive (disp,
// grounding flag, human label, normalized command, reasoning gist).
type decEvent struct {
	src       client.EventItem
	disp      string
	grounding bool
	label     string
	command   string
	gist      string
}

// Decorate computes disp/grounding/label/command/gist for every event, in place order. The grounding
// boundary mirrors the server's negative-seq band, ended by the band's terminal tool (submit_selection
// / grounding_aborted) so a main loop that inherited a negative seq still numbers as main.
func decorate(events []client.EventItem) []decEvent {
	out := make([]decEvent, 0, len(events))
	p, n := 0, 0
	inGrounding := true
	for _, e := range events {
		grounding := inGrounding && e.Seq < 0
		if !grounding || e.Tool == "submit_selection" || e.Tool == "grounding_aborted" {
			inGrounding = false
		}
		d := decEvent{src: e, grounding: grounding}
		if grounding {
			p++
			d.disp = "P" + strconv.Itoa(p)
		} else {
			n++
			d.disp = strconv.Itoa(n)
		}
		d.command, d.label = commandAndLabel(e)
		d.gist = gist(e.Reasoning, 100)
		out = append(out, d)
	}
	return out
}

// commandAndLabel derives the normalized command line + human label for one event by tool. Bash carries
// its command verbatim; reply/submit_selection/grounding_aborted summarize their structured args; any
// other tool falls back to the server's label/tool.
func commandAndLabel(e client.EventItem) (string, string) {
	switch e.Tool {
	case "bash":
		return e.Command, label(e.Command)
	case "reply":
		return summarizeArgs(e.Args), "reply"
	case "grounding_aborted":
		return argString(e.Args, "aborted"), "aborted"
	case "submit_selection":
		var a struct {
			Skip     bool              `json:"skip"`
			Selected []json.RawMessage `json:"selected"`
		}
		_ = json.Unmarshal(e.Args, &a)
		if a.Skip {
			return "skip (judged trivial)", "submit_selection"
		}
		return fmt.Sprintf("selected %d docs", len(a.Selected)), "submit_selection"
	default:
		lbl := e.Label
		if lbl == "" {
			lbl = e.Tool
		}
		return e.Command, lbl
	}
}

// --- label classification (ported from the shared renderer's _LABEL_RULES) ----------------------------

var cdPrefix = regexp.MustCompile(`^\s*cd\s+\S+\s*&&\s*`)

type labelRule struct {
	rx  *regexp.Regexp
	lbl string
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

// label classifies a bash command into a short human label (db query / http / read file / …), falling
// back to the first non-comment word.
func label(cmd string) string {
	c := cdPrefix.ReplaceAllString(cmd, "")
	for _, r := range labelRules {
		if r.rx.MatchString(c) {
			return r.lbl
		}
	}
	for _, line := range strings.Split(cmd, "\n") {
		words := strings.Fields(line)
		if len(words) > 0 && !strings.HasPrefix(words[0], "#") {
			return truncate(words[0], 20)
		}
	}
	return "bash"
}

// --- text helpers (ported from the shared renderer) ---------------------------------------------------

// gist returns the first sentence-ish of a reasoning blob: single line, cut at ". "/"; " before limit,
// else truncated with an ellipsis.
func gist(text string, limit int) string {
	line := strings.Join(strings.Fields(text), " ")
	for _, sep := range []string{". ", "; "} {
		if idx := strings.Index(line, sep); idx > 0 && idx < limit {
			return line[:idx+1]
		}
	}
	if len([]rune(line)) <= limit {
		return line
	}
	return string([]rune(line)[:limit-1]) + "…"
}

// cell renders one markdown table cell: single line, pipes escaped, truncated.
func cell(text string, limit int) string {
	line := strings.ReplaceAll(strings.Join(strings.Fields(text), " "), "|", "\\|")
	return truncate(line, limit)
}

func truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// fence wraps content in a backtick fence, bumping the run so embedded backticks can't break out.
func fence(text, lang string) string {
	body := strings.TrimRight(text, "\n")
	if body == "" {
		return "_(empty)_"
	}
	longest := 0
	for _, run := range backtickRuns(body) {
		if run > longest {
			longest = run
		}
	}
	ticks := strings.Repeat("`", max(3, longest+1))
	return ticks + lang + "\n" + body + "\n" + ticks
}

func backtickRuns(s string) []int {
	var runs []int
	run := 0
	for _, ch := range s {
		if ch == '`' {
			run++
		} else if run > 0 {
			runs = append(runs, run)
			run = 0
		}
	}
	if run > 0 {
		runs = append(runs, run)
	}
	return runs
}

func dur(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%02ds", ms/60_000, (ms%60_000)/1000)
	}
}

func cost(v float64) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("$%.4f", v)
}

// summarizeArgs renders a tool's structured args as "k=v k=v" (bools → yes/no), sorted for determinism.
func summarizeArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := m[k].(type) {
		case bool:
			yn := "no"
			if v {
				yn = "yes"
			}
			parts = append(parts, k+"="+yn)
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	return strings.Join(parts, " ")
}

// argString pulls a single string field out of a tool's args envelope.
func argString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Base returns the run-derived file stem `<run8>-<project>` used for both output filenames.
func Base(full *client.FullResponse) string {
	id := full.Run.RunID
	if len(id) > 8 {
		id = id[:8]
	}
	project := full.Run.Project
	if project == "" {
		project = "run"
	}
	return id + "-" + project
}

// JSONLName is the drill-down filename for a bundle.
func JSONLName(full *client.FullResponse) string { return Base(full) + ".jsonl" }

// IndexName is the markdown index filename for a bundle.
func IndexName(full *client.FullResponse) string { return Base(full) + ".md" }
