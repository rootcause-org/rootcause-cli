package debugdump

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

// RenderIndex builds the THIN markdown index: the run header, question, outcome gist, a main-loop
// timeline table (mechanical search/read steps omitted), auto-flagged anomalies, files read, an egress
// summary, and a Drill-down block of example jq calls. It is deliberately small — the JSONL is where the
// full detail lives; this file only says WHERE to look.
func RenderIndex(full *client.FullResponse) string {
	events := decorate(full.Events)
	r := full.Run
	jsonlName := JSONLName(full)

	var main, pre, polish []decEvent
	for _, e := range events {
		switch {
		case e.polish:
			polish = append(polish, e)
		case e.grounding:
			pre = append(pre, e)
		default:
			main = append(main, e)
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

	redacted := full.Redacted()

	add(fmt.Sprintf("# Run %s — %s · %s · %s", short8(r.RunID), orQ(r.Project), r.Status, r.Kind), "")
	// Lead with the withholding: everything below is a partial picture, and an index that merely LOOKS
	// empty is the failure mode this line exists to prevent.
	if redacted {
		add("> **Trace detail withheld (project-admin required).** Events, prompts, grounding, and egress "+
			"are not served to this token — the sections below are incomplete, not evidence of a quiet run.", "")
	}
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
	// A "0 main / 0 egress" count on a redacted bundle is a lie of omission — the counts were never served.
	if redacted && len(events) == 0 {
		add("- **Steps / Egress:** withheld")
	} else {
		steps := fmt.Sprintf("- **Steps:** %d main", len(main))
		if len(pre) > 0 {
			steps += fmt.Sprintf(" + %d grounding", len(pre))
		}
		steps += fmt.Sprintf(" · **Egress:** %d", len(r.Egress))
		if blocked > 0 {
			steps += fmt.Sprintf(" (%d blocked)", blocked)
		}
		add(steps)
	}
	if attachments, ok := attachmentSummary(events); ok {
		add("- **Attachments:** " + attachments)
	}
	if links, ok := linkSummary(events); ok {
		add("- **Links:** " + links)
	}
	add(fmt.Sprintf("- **Events (full, queryable):** `%s` — one JSON object per event; jq it (see Drill down).", jsonlName), "")

	add(renderProjectionInputs(r)...)
	add(renderGroundingSources(r.GroundingSources)...)
	// Skipped on a redacted bundle: the lead line already says the prompt planes were withheld, and an
	// "not captured" line here would blame retention for an access decision.
	if !redacted {
		add(renderPromptContext(r, jsonlName)...)
	}

	add("## Question", "")
	if r.Question != "" {
		add(fence(r.Question, ""))
	} else {
		add("_(none captured)_")
	}
	add("", "## Outcome", "")
	add(renderOutcome(r)...)
	// Directly under the draft it edited: the post-loop passes run AFTER the agent's terminal `reply`, so
	// a draft that doesn't match the reply row is otherwise unexplainable from the timeline.
	add(renderDraftCleanup(polish, jsonlName)...)

	// With no events served, a "Timeline" and a "Flags: _(none)_" section would both read as findings.
	// Skip them entirely — the lead line already said why they are missing.
	skipTimeline := redacted && len(events) == 0

	if !skipTimeline {
		add("", "## Timeline — main-loop steps (search/read steps omitted — see Files the run read)", "")
	}
	var rows []decEvent
	for _, e := range main {
		if e.label == "search files" || e.label == "read file" {
			continue
		}
		rows = append(rows, e)
	}
	switch {
	case skipTimeline:
	case len(rows) > 0:
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
	default:
		add("_(no main-loop tool calls recorded)_")
	}

	if !skipTimeline {
		add("", "## Flags", "")
		fl := flags(full, events)
		if len(fl) == 0 {
			add("_(none)_")
		} else {
			for _, f := range fl {
				add("- " + f)
			}
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
	add("", "For the full three-step opening context each model received, with per-section sources: "+
		"`rc dev context-export` (offline, operator).")
	add("")
	return strings.Join(L, "\n")
}

func renderProjectionInputs(r client.RunHeader) []string {
	if r.BrainResolved == "" && r.Tenant == "" && r.TenantSettings == "" && r.TenantSettingsCurrent == "" {
		return nil
	}
	var out []string
	out = append(out, "## Projection inputs", "")
	if r.BrainResolved != "" {
		out = append(out, fmt.Sprintf("- **Brain resolved:** `%s`", backtickSafe(r.BrainResolved)))
	}
	if r.Tenant != "" {
		out = append(out, fmt.Sprintf("- **Tenant:** `%s`", backtickSafe(r.Tenant)))
	}
	if strings.TrimSpace(r.TenantSettings) != "" {
		snap, err := client.ParseTenantSettingsSnapshot(r.TenantSettings)
		if err != nil {
			out = append(out, fmt.Sprintf("- **Tenant settings:** present (unparseable: `%s`)", backtickSafe(err.Error())))
		} else if snap != nil {
			out = append(out, fmt.Sprintf("- **Tenant settings:** source `%s` · synced_at `%s` · version `%s`",
				backtickSafe(orQ(snap.Source)), backtickSafe(orQ(snap.SyncedAt)), backtickSafe(orQ(snap.Version))))
			if selectors := digest.BranchSelectorValues(snap.Settings); len(selectors) > 0 {
				out = append(out, fmt.Sprintf("- **Branch selectors:** %s", selectorSummary(selectors)))
			}
		}
	}
	if drift, err := digest.TenantSettingsDrift(r.TenantSettings, r.TenantSettingsCurrent); err == nil && len(drift) > 0 {
		out = append(out, "", "### Current drift", "")
		out = append(out, "Careful: when this run happened, these tenant settings differed from the current config.")
		for _, d := range drift {
			out = append(out, fmt.Sprintf("- `%s`: then `%s`; now `%s`", backtickSafe(d.Key), backtickSafe(d.Then), backtickSafe(d.Now)))
		}
	}
	out = append(out, "")
	return out
}

// renderPromptContext renders the run's persisted prompt context: the system prompt's section map (WHAT
// was a candidate and which gate turned it on), and the orientation turns' block index. Texts stay in
// the JSONL — this is navigation. When the server served nothing, it says so in one line: silence here
// would read as "the model got nothing", a different and false fact.
func renderPromptContext(r client.RunHeader, jsonlName string) []string {
	out := []string{"## Prompt context (what the model was handed)", ""}
	if !r.ContextCaptured() {
		return append(out, "- **Not captured** — this run predates per-run context capture, or its context "+
			"aged past the 7-day retention window. The `system_prompt` in the JSONL is still the joined "+
			"prompt; its per-section gates are gone.", "")
	}
	sections, secErr := client.ParsePromptSections(r.PromptSections)
	blocks, blkErr := client.ParseManifestBlocks(r.ManifestBlocks)

	on := 0
	for _, s := range sections {
		if s.On {
			on++
		}
	}
	out = append(out, fmt.Sprintf("- **Captured:** yes (schema v%d) · %d/%d system-prompt sections on",
		r.ContextSchemaVersion, on, len(sections)))
	out = append(out, fmt.Sprintf("- **Bootstrap turn:** %s · **Pre-selected turn:** %s",
		turnSize(r.BootstrapTurn), turnSize(r.PreselectedTurn)))

	if secErr != nil {
		out = append(out, fmt.Sprintf("- **Sections:** unreadable (`%s`)", backtickSafe(secErr.Error())))
	} else if len(sections) > 0 {
		out = append(out, "", "### System-prompt sections", "",
			"| section | on | gate | chars |", "|---|---|---|---|")
		for _, s := range sections {
			out = append(out, fmt.Sprintf("| `%s` | %s | %s | %s |",
				backtickSafe(s.ID), yesNo(s.On), cell(s.Gate, 80), charCount(s.On, len(s.Text))))
		}
	}

	if blkErr != nil {
		out = append(out, "", fmt.Sprintf("- **Bootstrap blocks:** unreadable (`%s`)", backtickSafe(blkErr.Error())))
	} else if len(blocks) > 0 {
		out = append(out, "", "### Bootstrap blocks (bodies are inside the bootstrap turn)", "",
			"| path | presence | chars | truncated | authoritative |", "|---|---|---|---|---|")
		for _, b := range blocks {
			out = append(out, fmt.Sprintf("| `%s` | %s | %d | %s | %s |",
				backtickSafe(b.Path), orQ(b.Presence), b.Chars, yesNo(b.Truncated), yesNo(b.Authoritative)))
		}
	}
	out = append(out, "", "Full texts (never in this index) live in the JSONL run header:", "",
		fence(strings.Join([]string{
			fmt.Sprintf(`jq -r '.prompt_sections[]? | select(.on).text' %s   # the gated prompt, section by section`, jsonlName),
			fmt.Sprintf(`jq -r '.prompt_sections[]? | select(.on|not) | .id + " — " + .gate' %s   # what stayed OFF, and why`, jsonlName),
			fmt.Sprintf(`jq -r '.bootstrap_turn // "(none)"' %s   # the pasted orientation turn, verbatim`, jsonlName),
			fmt.Sprintf(`jq -r '.preselected_turn // "(none)"' %s   # the pre-pass's pre-selected ranges`, jsonlName),
		}, "\n"), "sh"), "")
	return out
}

// turnSize describes one orientation turn without printing it: "" is a real state (the turn is not
// sent), distinct from a turn that is present but empty.
func turnSize(s string) string {
	if s == "" {
		return "not sent"
	}
	return fmt.Sprintf("%d chars, %d lines", len(s), strings.Count(s, "\n")+1)
}

func charCount(on bool, n int) string {
	if !on {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// renderDraftCleanup renders the post-loop polish passes (server seq band 4M+): each pass, whether it
// fired, and what it did to the draft. Terse by design — the before/after markdown and the refused
// rewrite live in the JSONL. Omitted entirely when no pass ran (a pre-capture run, or a run with no
// draft): unlike the prompt context there is nothing to degrade, only nothing to show.
func renderDraftCleanup(polish []decEvent, jsonlName string) []string {
	if len(polish) == 0 {
		return nil
	}
	out := []string{"", "## Draft cleanup — post-loop polish passes", "",
		"These run AFTER the agent's terminal `reply`, so a shipped draft can differ from the one the " +
			"timeline ends on.", "",
		"| # | pass | status | changed | detail |", "|---|---|---|---|---|"}
	for _, e := range polish {
		p := parsePolish(e.src)
		out = append(out, fmt.Sprintf("| %s | %s | %s | %s | %s |",
			e.disp, orQ(p.Pass), orQ(p.Status), yesNo(p.Changed), polishDetail(p)))
	}
	out = append(out, "", fence(strings.Join([]string{
		fmt.Sprintf(`jq -r 'select(.disp=="%s").args.before' %s   # the draft as the agent wrote it`, polish[0].disp, jsonlName),
		fmt.Sprintf(`jq -r 'select(.disp=="%s").args.after'  %s   # the draft as it shipped`, polish[0].disp, jsonlName),
		fmt.Sprintf(`jq -r 'select(.args.rejected_diff).args.rejected_diff' %s   # a rewrite the pass refused`, jsonlName),
	}, "\n"), "sh"), "")
	return out
}

// polishDetail says what the pass DID in one cell: the rewrite's size delta, the refused diff, or why it
// was a no-op. Sizes only — no cost, tokens or model ever ride on a polish row.
func polishDetail(p polishPass) string {
	var parts []string
	switch {
	case p.RejectedDiff != "":
		// A refusal keeps the agent's draft: reporting the (empty) "after" as a size delta would read as a
		// rewrite that blanked the reply.
		parts = append(parts, "refused, draft kept as-is: `"+cell(p.RejectedDiff, 90)+"`")
	case p.Before != "" || p.After != "":
		parts = append(parts, fmt.Sprintf("markdown %d → %d chars", len(p.Before), len(p.After)))
	case !p.Called:
		parts = append(parts, "pass not called")
	}
	if p.HTMLChanged {
		parts = append(parts, "html changed")
	}
	if p.Trigger != "" {
		parts = append(parts, "trigger "+p.Trigger)
	}
	if p.Flagged > 0 {
		parts = append(parts, fmt.Sprintf("%d flagged", p.Flagged))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " · ")
}

func renderGroundingSources(gs *client.GroundingSources) []string {
	if gs == nil {
		return nil
	}
	out := []string{"## Grounding sources", ""}
	if !gs.Captured {
		reason := gs.Reason
		if reason == "" {
			reason = "unknown"
		}
		return append(out, fmt.Sprintf("- **Captured:** no (`%s`)", backtickSafe(reason)), "")
	}
	out = append(out, fmt.Sprintf("- **Captured:** yes · captured_at `%s` · current_checked_at `%s`",
		backtickSafe(orQ(gs.CapturedAt)), backtickSafe(orQ(gs.CurrentCheckedAt))))
	driftCount := digest.GroundingSourceDriftCount(gs)
	attention := digest.GroundingSourceAttentionCount(gs)
	if driftCount > 0 || attention > 0 {
		out = append(out, fmt.Sprintf("- **Attention:** %d source(s), %d drift field(s)", attention, driftCount))
	}
	if len(gs.Sources) == 0 {
		return append(out, "- _(no source rows)_", "")
	}
	for _, s := range digest.SortGroundingSources(gs.Sources) {
		out = append(out, "", fmt.Sprintf("### `%s`", backtickSafe(groundingSourceName(s))))
		if s.MountPath != "" {
			out = append(out, fmt.Sprintf("- **Source:** `%s`", backtickSafe(s.MountPath)))
		}
		if detail := groundingDetails(s.Details); detail != "" {
			out = append(out, fmt.Sprintf("- **Details:** %s", detail))
		}
		out = append(out, fmt.Sprintf("- **Snapshot:** %s", groundingSnapshot(s)))
		out = append(out, fmt.Sprintf("- **Sync:** %s", groundingSync(s)))
		out = append(out, fmt.Sprintf("- **Current drift:** %s", groundingCurrentDrift(s)))
	}
	out = append(out, "")
	return out
}

func groundingSourceName(s client.GroundingSource) string {
	if s.Kind == "" {
		return s.Name
	}
	if s.Name == "" {
		return s.Kind
	}
	return s.Kind + "/" + s.Name
}

func groundingSnapshot(s client.GroundingSource) string {
	parts := []string{
		kv("ref", s.Ref),
		kv("sha", shortSHA(s.CommitSHA)),
		kv("committed", s.CommittedAt),
		kvBool("mounted", s.Mounted),
	}
	return joinNonEmpty(parts, " · ")
}

func groundingSync(s client.GroundingSource) string {
	parts := []string{
		kvBool("configured", s.Configured),
		kvBool("available", s.Available),
		kv("state", s.State),
		kv("last_ok", s.LastOKAt),
		kv("last_attempt", s.LastAttemptAt),
	}
	return joinNonEmpty(parts, " · ")
}

func groundingCurrentDrift(s client.GroundingSource) string {
	var parts []string
	if len(s.Drift) == 0 {
		parts = append(parts, "none")
	} else {
		quoted := make([]string, 0, len(s.Drift))
		for _, d := range s.Drift {
			quoted = append(quoted, "`"+backtickSafe(d)+"`")
		}
		parts = append(parts, strings.Join(quoted, ", "))
	}
	if s.Current != nil {
		parts = append(parts,
			kv("now_ref", s.Current.Ref),
			kv("now_sha", shortSHA(s.Current.CommitSHA)),
			kv("state", s.Current.State),
			kv("last_ok", s.Current.LastOKAt),
		)
	}
	return joinNonEmpty(parts, " · ")
}

func groundingDetails(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	var parts []string
	for _, k := range []string{"provider", "locale"} {
		if v, ok := details[k]; ok {
			parts = append(parts, kv(k, fmt.Sprint(v)))
		}
	}
	if scope, ok := details["scope"].(map[string]any); ok {
		parts = append(parts, "scope="+backtickSafe(scopeSummary(scope)))
	}
	return joinNonEmpty(parts, " · ")
}

func scopeSummary(scope map[string]any) string {
	var parts []string
	for _, k := range []string{
		"mode", "tenant",
		"project_total", "project_visible", "project_hidden",
		"tenant_total", "total_visible",
		"visible", "total",
		"hidden", "scoped",
	} {
		if v, ok := scope[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) == 0 {
		return "present"
	}
	return strings.Join(parts, " ")
}

func kv(key, value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf("%s `%s`", key, backtickSafe(value))
}

func kvBool(key string, value bool) string {
	if value {
		return key + " `yes`"
	}
	return key + " `no`"
}

func joinNonEmpty(parts []string, sep string) string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, sep)
}

// renderOutcome shows the draft gist (first 8 lines), action proposals, the FULL note bodies, or a "no
// callback" marker.
func renderOutcome(r client.RunHeader) []string {
	if r.Draft == "" && len(r.Notes) == 0 && len(r.ProposedActions) == 0 && len(r.Metadata) == 0 {
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
	if len(r.ProposedActions) > 0 {
		out = append(out, fmt.Sprintf("**Proposed actions** (%d):", len(r.ProposedActions)), "")
		for _, a := range r.ProposedActions {
			id := firstNonEmpty(a.ID, "?")
			slug := firstNonEmpty(a.Slug, "?")
			label := firstNonEmpty(a.Label, "?")
			line := fmt.Sprintf("- `%s` · `%s` · %s", backtickSafe(id), backtickSafe(slug), label)
			if a.Description != "" {
				line += " — " + a.Description
			}
			out = append(out, line)
			if a.URL != "" {
				out = append(out, "  url: "+a.URL)
			}
		}
		out = append(out, "")
	}
	// Notes render in FULL. They are short by construction and carry the reviewer-facing 👀/📊 lines; a
	// first-sentence gist hid those in the index and cost two reviewer round-trips in the 2026-08-12 audit.
	for _, n := range r.Notes {
		key := ""
		if n.Key != "" {
			key = " `" + n.Key + "`"
		}
		out = append(out, "**Note"+key+":**", "", fence(strings.TrimSpace(n.Body), ""), "")
	}
	return out
}

// --- index formatters --------------------------------------------------------------------------------

func selectorSummary(vals []digest.TenantSettingValue) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("`%s=%s`", backtickSafe(v.Key), backtickSafe(v.Value)))
	}
	return strings.Join(parts, " · ")
}

func backtickSafe(s string) string {
	return strings.ReplaceAll(s, "`", "'")
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

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
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

// --- header summaries (link + attachment traces, rendered from the last reply/compose args) ----------

// linkSummary renders the link-validator verdicts carried by the last reply/compose call as one
// index-header line ("N checked · N passed · …" plus a per-link detail). Second return is false when
// no such call carried a `links` envelope, so the header omits the line entirely.
func linkSummary(events []decEvent) (string, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].src.Tool != "reply" && events[i].src.Tool != "compose" {
			continue
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal(events[i].src.Args, &envelope) != nil {
			continue
		}
		raw, ok := envelope["links"]
		if !ok {
			return "", false
		}
		var links []linkTrace
		if json.Unmarshal(raw, &links) != nil {
			return "", false
		}
		passed, removed := 0, 0
		parts := make([]string, 0, len(links))
		for _, link := range links {
			switch link.Verdict {
			case "pass":
				passed++
			case "fail":
				removed++
			}
			status := "no HTTP status"
			if link.Status > 0 {
				status = fmt.Sprintf("HTTP %d", link.Status)
			}
			parts = append(parts, fmt.Sprintf("`%s` · %s · %s · %d ms",
				strings.ReplaceAll(link.URL, "`", "\\`"), link.Verdict, status, link.MS))
		}
		checked := passed + removed
		summary := fmt.Sprintf("%d checked · %d passed · %d removed · %d untouched",
			checked, passed, removed, len(links)-checked)
		if len(parts) > 0 {
			summary += " — " + strings.Join(parts, "; ")
		}
		return summary, true
	}
	return "", false
}

// attachmentSummary renders what the last reply call declared vs actually shipped as one index-header
// line, naming each attachment and any drop reason.
func attachmentSummary(events []decEvent) (string, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].src.Tool != "reply" {
			continue
		}
		var args struct {
			Attachments []attachmentTrace `json:"attachments"`
		}
		_ = json.Unmarshal(events[i].src.Args, &args)
		shipped := 0
		parts := make([]string, 0, len(args.Attachments))
		for _, a := range args.Attachments {
			state := a.Status
			if state == "shipped" {
				shipped++
			} else if a.DropReason != "" {
				state += ": " + a.DropReason
			}
			mime := a.MimeType
			if mime == "" {
				mime = "unknown MIME"
			}
			parts = append(parts, fmt.Sprintf("`%s` · %d bytes · `%s` · `%s` · %s",
				a.Filename, a.SizeBytes, mime, a.Path, state))
		}
		summary := fmt.Sprintf("%d declared · %d shipped · %d dropped", len(args.Attachments), shipped, len(args.Attachments)-shipped)
		if len(parts) > 0 {
			summary += " — " + strings.Join(parts, "; ")
		}
		return summary, true
	}
	return "", false
}
