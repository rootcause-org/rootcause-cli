package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The splitter turns a downloaded harvest corpus (front-matter + one `## ` section per thread) into a
// per-thread file tree plus an INDEX. It is deterministic string processing kept out of the command so
// it's unit-testable without a server. The corpus format is VERSIONED: supportedHarvestCorpusFormats
// enumerates the exact tags we parse; anything else fails loudly so a future server render change can't
// be silently mis-parsed.

// supportedHarvestCorpusFormats are the server render shapes this splitter understands. Keep this list
// in sync with the parser fixtures and rc self doctor, which advertises the same local capability.
var supportedHarvestCorpusFormats = [...]string{"v1", "v2", "v3"}

// splitThread is one parsed `## ` section: its header index, subject, span/participants (for the
// index), and the verbatim section body (header line included) to write out.
type splitThread struct {
	idx          int
	subject      string
	spanStart    string   // yyyy-mm-dd of the thread's span start, or "" if none
	participants []string // participant addresses from the **Participants:** line
	msgCount     int      // number of `**addr (date):**` message headers in the section
	body         string   // the full section text, starting at "## "
}

// splitCorpus is the parsed corpus: the front-matter metadata the index needs + the threads.
type splitCorpus struct {
	mailbox     string
	harvestedAt string
	cleaned     string
	diagnostics *splitDiagnostics
	threads     []splitThread
}

type splitDiagnostics struct {
	SchemaVersion    int                  `json:"schema_version"`
	Available        bool                 `json:"available"`
	AcceptedCount    int                  `json:"accepted_count"`
	RejectionReasons map[string]int       `json:"rejection_reasons"`
	ActorTypes       map[string]int       `json:"actor_types"`
	InitiationTypes  map[string]int       `json:"initiation_types"`
	CandidateBands   []splitCandidateBand `json:"candidate_bands"`
	Deduplicated     int                  `json:"deduplicated"`
}

type splitCandidateBand struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Count int    `json:"count"`
}

var (
	// threadHeaderRe matches a section header line: `## <subject> — #<idx>`. The subject may itself
	// contain `#`, so the idx is anchored to the trailing ` — #<n>` and the subject is everything before.
	threadHeaderRe = regexp.MustCompile(`(?m)^## (.*) — #(\d+)[ \t\r]*$`)
	// messageHeaderRe matches one message header inside a section: `**addr (date):**`.
	messageHeaderRe = regexp.MustCompile(`(?m)^\*\*(.+?) \((.+?)\):\*\*`)
	// spanRe pulls the span start date (yyyy-mm-dd) off a `**Span:** <start> → <end>` line.
	spanRe = regexp.MustCompile(`(?m)^\*\*Span:\*\*\s*(\d{4}-\d{2}-\d{2})`)
	// messageDateRe pulls the first rendered message date. V2 deliberately omits the v1 Span metadata,
	// so its first chronological message supplies the index/file month.
	messageDateRe = regexp.MustCompile(`(?m)^\*\*.+? \((\d{4}-\d{2}-\d{2})\):\*\*`)
	// slugNonAlnumRe collapses any run of non-alphanumeric characters to a single `-` for the file slug.
	slugNonAlnumRe = regexp.MustCompile(`[^a-z0-9]+`)
)

// parseCorpus parses a harvest corpus string into its front-matter + thread sections, verifying the
// harvest_format version. It errors loudly on a missing/foreign version so a drifted server render is a
// hard failure, never a silent mis-parse.
func parseCorpus(corpus string) (*splitCorpus, error) {
	fm, body, err := splitFrontMatter(corpus)
	if err != nil {
		return nil, err
	}
	format := fm["harvest_format"]
	if format == "" {
		return nil, fmt.Errorf("corpus front-matter missing harvest_format (expected one of %s) — refusing to parse a possibly-drifted format", supportedHarvestCorpusFormatList())
	}
	if !supportsHarvestCorpusFormat(format) {
		return nil, fmt.Errorf("unsupported harvest_format %q (this CLI understands %s) — the server render changed; run `rc self update`", format, supportedHarvestCorpusFormatList())
	}

	out := &splitCorpus{
		mailbox:     fm["mailbox"],
		harvestedAt: fm["harvested_at"],
		cleaned:     fm["cleaned"],
	}
	expected, err := expectedCorpusThreadCount(fm, format)
	if err != nil {
		return nil, err
	}
	if format == "v3" {
		out.diagnostics, err = parseV3Diagnostics(fm, expected)
		if err != nil {
			return nil, err
		}
	}

	// Only renderer thread headers (`## <subject> — #<n>`) begin sections. Message bodies are arbitrary
	// Markdown and may contain ordinary H2 lines; those must stay inside their owning thread.
	sections := splitThreadSections(body)
	if first := threadHeaderRe.FindStringIndex(body); first != nil {
		if prefix := strings.TrimSpace(body[:first[0]]); prefix != "" && !isV3DiagnosticsPreamble(format, prefix) {
			return nil, fmt.Errorf("harvest_format %s has content before thread #1 — refusing a partial split", format)
		}
	} else if prefix := strings.TrimSpace(body); prefix != "" && !isV3DiagnosticsPreamble(format, prefix) {
		return nil, fmt.Errorf("harvest_format %s declares %d threads but has no valid thread sections — refusing a partial split", format, expected)
	}
	for _, sec := range sections {
		m := threadHeaderRe.FindStringSubmatch(sec)
		if m == nil {
			// A stray block that isn't a thread header (shouldn't happen for v1) — skip rather than guess.
			continue
		}
		idx := atoiSafe(m[2])
		wantIdx := len(out.threads) + 1
		if idx != wantIdx {
			return nil, fmt.Errorf("harvest_format %s thread index is #%d, expected #%d — refusing a partial split", format, idx, wantIdx)
		}
		spanStart := firstSubmatch(spanRe, sec)
		if (format == "v2" || format == "v3") && spanStart == "" {
			spanStart = firstSubmatch(messageDateRe, sec)
		}
		out.threads = append(out.threads, splitThread{
			idx:          idx,
			subject:      strings.TrimSpace(m[1]),
			spanStart:    spanStart,
			participants: parseParticipants(sec),
			msgCount:     len(messageHeaderRe.FindAllStringIndex(sec, -1)),
			body:         strings.TrimRight(sec, "\n") + "\n",
		})
	}
	if len(out.threads) != expected {
		return nil, fmt.Errorf("harvest_format %s declares %d threads but %d valid thread sections were found — refusing a partial split", format, expected, len(out.threads))
	}
	return out, nil
}

func isV3DiagnosticsPreamble(format, prefix string) bool {
	return format == "v3" && strings.HasPrefix(prefix, "## Harvest diagnostics\n")
}

var diagnosticCategoryRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func parseV3Diagnostics(frontMatter map[string]string, uniqueContent int) (*splitDiagnostics, error) {
	required := []string{"accepted_count", "rejection_reasons", "actor_types", "initiation_types", "candidate_bands", "deduplicated"}
	for _, field := range required {
		if _, ok := frontMatter[field]; !ok {
			return nil, fmt.Errorf("harvest_format v3 front-matter missing %s — refusing to split incomplete diagnostics", field)
		}
	}
	accepted, err := parseNonNegativeInt(frontMatter["accepted_count"], "accepted_count")
	if err != nil {
		return nil, err
	}
	if accepted < uniqueContent {
		return nil, fmt.Errorf("harvest_format v3 accepted_count %d is smaller than unique_content %d", accepted, uniqueContent)
	}
	deduplicated, err := parseNonNegativeInt(frontMatter["deduplicated"], "deduplicated")
	if err != nil {
		return nil, err
	}
	rejections, err := parseDiagnosticCounts(frontMatter["rejection_reasons"], "rejection_reasons")
	if err != nil {
		return nil, err
	}
	actors, err := parseDiagnosticCounts(frontMatter["actor_types"], "actor_types")
	if err != nil {
		return nil, err
	}
	initiations, err := parseDiagnosticCounts(frontMatter["initiation_types"], "initiation_types")
	if err != nil {
		return nil, err
	}
	bands, err := parseDiagnosticBands(frontMatter["candidate_bands"])
	if err != nil {
		return nil, err
	}
	return &splitDiagnostics{
		SchemaVersion: 1, Available: true, AcceptedCount: accepted, RejectionReasons: rejections,
		ActorTypes: actors, InitiationTypes: initiations, CandidateBands: bands, Deduplicated: deduplicated,
	}, nil
}

func parseNonNegativeInt(raw, field string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || strconv.Itoa(value) != raw {
		return 0, fmt.Errorf("harvest_format v3 front-matter has invalid %s %q", field, raw)
	}
	return value, nil
}

func parseDiagnosticCounts(raw, field string) (map[string]int, error) {
	var counts map[string]int
	if err := json.Unmarshal([]byte(raw), &counts); err != nil || counts == nil {
		return nil, fmt.Errorf("harvest_format v3 %s must be a JSON object of category counts", field)
	}
	for category, count := range counts {
		if !diagnosticCategoryRe.MatchString(category) || count < 0 {
			return nil, fmt.Errorf("harvest_format v3 %s contains invalid category/count %q=%d", field, category, count)
		}
	}
	return counts, nil
}

func parseDiagnosticBands(raw string) ([]splitCandidateBand, error) {
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &objects); err != nil || objects == nil {
		return nil, fmt.Errorf("harvest_format v3 candidate_bands must be a JSON array")
	}
	bands := make([]splitCandidateBand, 0, len(objects))
	var previousEnd time.Time
	for i, object := range objects {
		if len(object) != 3 || object["start"] == nil || object["end"] == nil || object["count"] == nil {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands[%d] must contain only start/end/count", i)
		}
		var band splitCandidateBand
		if err := json.Unmarshal(object["start"], &band.Start); err != nil {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands[%d].start must be a UTC timestamp", i)
		}
		if err := json.Unmarshal(object["end"], &band.End); err != nil {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands[%d].end must be a UTC timestamp", i)
		}
		if err := json.Unmarshal(object["count"], &band.Count); err != nil || band.Count < 0 {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands[%d].count must be non-negative", i)
		}
		start, errStart := time.Parse(time.RFC3339, band.Start)
		end, errEnd := time.Parse(time.RFC3339, band.End)
		if errStart != nil || !strings.HasSuffix(band.Start, "Z") || errEnd != nil || !strings.HasSuffix(band.End, "Z") || !start.Before(end) {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands[%d] must be a non-empty UTC [start,end) band", i)
		}
		if !previousEnd.IsZero() && start.Before(previousEnd) {
			return nil, fmt.Errorf("harvest_format v3 candidate_bands must be sorted and non-overlapping")
		}
		previousEnd = end
		bands = append(bands, band)
	}
	return bands, nil
}

func expectedCorpusThreadCount(frontMatter map[string]string, format string) (int, error) {
	field := "threads"
	if format == "v2" || format == "v3" {
		field = "unique_content"
	}
	raw, ok := frontMatter[field]
	if !ok || strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("harvest_format %s front-matter missing %s — refusing to split an incomplete corpus", format, field)
	}
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || count < 0 {
		return 0, fmt.Errorf("harvest_format %s front-matter has invalid %s %q — refusing to split an incomplete corpus", format, field, raw)
	}
	return count, nil
}

func supportsHarvestCorpusFormat(format string) bool {
	for _, supported := range supportedHarvestCorpusFormats {
		if format == supported {
			return true
		}
	}
	return false
}

func supportedHarvestCorpusFormatList() string {
	return strings.Join(supportedHarvestCorpusFormats[:], ", ")
}

// splitFrontMatter separates a leading `---\n…\n---` YAML front-matter block from the body, returning
// the parsed key→value map (flat scalars only, which is all a harvest emits) and the remaining body.
func splitFrontMatter(corpus string) (map[string]string, string, error) {
	s := strings.TrimPrefix(corpus, "\ufeff") // tolerate a leading UTF-8 BOM
	if !strings.HasPrefix(s, "---") {
		return nil, "", fmt.Errorf("corpus has no front-matter block (expected a leading `---`)")
	}
	// Find the closing fence: the first `\n---` after the opening line.
	rest := s[len("---"):]
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("corpus front-matter is not closed (expected a trailing `---`)")
	}
	fmBlock := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r")
	body = strings.TrimPrefix(body, "\n")

	fm := map[string]string{}
	for _, line := range strings.Split(fmBlock, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fm[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return fm, body, nil
}

// splitThreadSections splits only at anchored renderer thread headers. Arbitrary Markdown H2 lines in a
// message body remain part of the section. parseCorpus separately rejects non-whitespace text before
// the first header and validates the complete section count.
func splitThreadSections(body string) []string {
	matches := threadHeaderRe.FindAllStringIndex(body, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for i, match := range matches {
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		out = append(out, body[match[0]:end])
	}
	return out
}

// parseParticipants pulls the addresses off the `**Participants:** a, b, c` line of a section.
func parseParticipants(sec string) []string {
	const marker = "**Participants:**"
	for _, line := range strings.Split(sec, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, marker) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, marker))
		if rest == "" {
			return nil
		}
		var out []string
		for _, p := range strings.Split(rest, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// participantDomains reduces a participant list to its unique email domains (sorted), the compact
// signal the index shows — the addresses themselves would bloat the row and leak more PII than needed.
func participantDomains(participants []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range participants {
		if i := strings.LastIndexByte(p, '@'); i >= 0 && i < len(p)-1 {
			d := strings.ToLower(p[i+1:])
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// threadFileName builds the per-thread file basename: `<yyyy-mm>--<slug>--<idx>.md`. The month comes
// from the thread's span start, falling back to fallbackMonth (the corpus harvested_at month) when the
// thread carried no span.
func threadFileName(t splitThread, fallbackMonth string) string {
	month := monthOf(t.spanStart)
	if month == "" {
		month = fallbackMonth
	}
	if month == "" {
		month = "unknown"
	}
	return fmt.Sprintf("%s--%s--%d.md", month, slugify(t.subject), t.idx)
}

// monthOf returns the yyyy-mm prefix of a yyyy-mm-dd date, or "" if the date is empty/too short.
func monthOf(date string) string {
	if len(date) >= 7 {
		return date[:7]
	}
	return ""
}

// slugify lowercases the subject, maps every run of non-alphanumerics to a single `-`, trims leading/
// trailing `-`, and caps the length (~40 chars) so filenames stay short and portable. An empty result
// becomes "thread".
func slugify(subject string) string {
	s := strings.ToLower(strings.TrimSpace(subject))
	s = slugNonAlnumRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		return "thread"
	}
	return s
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
