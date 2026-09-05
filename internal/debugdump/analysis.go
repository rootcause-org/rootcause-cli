// analysis.go holds the decomposer's only judgement calls: the anomaly policy behind the index's
// "Flags" section, plus the mount-path rule behind "Files read". Everything else in this package is a
// mechanical move of server data into JSONL or Markdown; these thresholds are ours, so they live in one
// file where they can be argued with. Today's policy:
//   - run errored, or no stored callback at all (no draft, notes or metadata);
//   - a step whose status is not "ok", or whose exit code is non-zero — except a grep/rg miss with empty
//     stderr (benignGrepMiss), which is "no match", not a failure;
//   - output mentioning EGRESS_BLOCKED, and any egress host the sandbox actually blocked;
//   - stdout larger than 20 KB on one step;
//   - the same normalized bash command run more than once — possible flailing;
//   - a turn slower than 4x the median turn and at least 1 s, over at least 4 timed turns.
//
// Polish rows (synthetic post-loop draft cleanup) are dropped before any of this: they are host
// bookkeeping, not turns the agent took, so they neither flag nor skew the median.

package debugdump

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

var grepRx = regexp.MustCompile(`^\s*(rg|grep|egrep|fgrep)\b`)

// flags surfaces where "why did it do that" questions are likely to live: errors, failed steps, blocked
// egress, repeated commands, one turn dominating the wall clock, large output. A trimmed port of the
// shared renderer's flags().
func flags(full *client.FullResponse, events []decEvent) []string {
	r := full.Run
	var out []string
	// Polish rows are host bookkeeping, not turns: their non-"ok" status would read as a failed step and
	// their (absent) duration would skew the median. They have their own section.
	kept := events[:0:0]
	for _, e := range events {
		if !e.polish {
			kept = append(kept, e)
		}
	}
	events = kept
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

	// Duration spikes: one turn that dominated the wall clock — the "why did this run take so long"
	// pointer. Needs ≥4 timed turns for a meaningful median, and ≥1s so millisecond noise never flags.
	var durs []float64
	for _, e := range events {
		if e.src.DurationMs > 0 {
			durs = append(durs, float64(e.src.DurationMs))
		}
	}
	if len(durs) >= 4 {
		med := median(durs)
		for _, e := range events {
			d := float64(e.src.DurationMs)
			if d > 0 && med > 0 && d > 4*med && d >= 1000 {
				out = append(out, fmt.Sprintf("[%s] slow turn %s (%.0f× median turn)", e.disp, dur(e.src.DurationMs), d/med))
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

// pathRx matches the run's read-only mounts in commands — the bridge to "what did the run read". ALL
// mounts must be listed: a missing one (`/tenant` was absent until 2026-08-13) renders an index that
// silently claims the run never opened a tenant file, which reads as evidence in an audit.
// The leading (^|[^\w-]) group emulates the reference renderer's negative lookbehind (RE2 has none): a
// path must not be glued onto a preceding word/hyphen char, so `foo/brain/x.py` isn't mis-read as a path.
// Matching is global over the WHOLE command string, so every clause of a `;`/`&&`-chained or `for f in
// … ; do … done` command contributes its paths, not just the first.
// Group 1 is the boundary (discarded); group 2 is the path.
var pathRx = regexp.MustCompile(`(^|[^\w-])(/(?:brain|tenant|mirrors|kb|tmp/rc-context)/[A-Za-z0-9._/@%+-]*[A-Za-z0-9_])`)

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
