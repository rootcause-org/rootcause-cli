package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The splitter turns a downloaded harvest corpus (front-matter + one `## ` section per thread) into a
// per-thread file tree plus an INDEX. It is deterministic string processing kept out of the command so
// it's unit-testable without a server. The corpus format is VERSIONED: harvestFormatVersion is the only
// tag we parse; anything else fails loudly so a future server render change can't be silently
// mis-parsed.

// harvestFormatVersion is the corpus front-matter version tag this splitter understands.
const harvestFormatVersion = "v1"

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
	threads     []splitThread
}

var (
	// threadHeaderRe matches a section header line: `## <subject> — #<idx>`. The subject may itself
	// contain `#`, so the idx is anchored to the trailing ` — #<n>` and the subject is everything before.
	threadHeaderRe = regexp.MustCompile(`(?m)^## (.*) — #(\d+)\s*$`)
	// messageHeaderRe matches one message header inside a section: `**addr (date):**`.
	messageHeaderRe = regexp.MustCompile(`(?m)^\*\*(.+?) \((.+?)\):\*\*`)
	// spanRe pulls the span start date (yyyy-mm-dd) off a `**Span:** <start> → <end>` line.
	spanRe = regexp.MustCompile(`(?m)^\*\*Span:\*\*\s*(\d{4}-\d{2}-\d{2})`)
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
	if v := fm["harvest_format"]; v != harvestFormatVersion {
		if v == "" {
			return nil, fmt.Errorf("corpus front-matter missing harvest_format (expected %s) — refusing to parse a possibly-drifted format", harvestFormatVersion)
		}
		return nil, fmt.Errorf("unsupported harvest_format %q (this CLI understands %s) — the server render changed; run `rc self update`", v, harvestFormatVersion)
	}

	out := &splitCorpus{
		mailbox:     fm["mailbox"],
		harvestedAt: fm["harvested_at"],
		cleaned:     fm["cleaned"],
	}

	// Split the body on the `\n## ` boundary so subjects containing `#` don't false-split. Each section
	// is re-prefixed with "## " (stripped by the split) except a leading section already starting there.
	sections := splitThreadSections(body)
	for _, sec := range sections {
		m := threadHeaderRe.FindStringSubmatch(sec)
		if m == nil {
			// A stray block that isn't a thread header (shouldn't happen for v1) — skip rather than guess.
			continue
		}
		idx := atoiSafe(m[2])
		out.threads = append(out.threads, splitThread{
			idx:          idx,
			subject:      strings.TrimSpace(m[1]),
			spanStart:    firstSubmatch(spanRe, sec),
			participants: parseParticipants(sec),
			msgCount:     len(messageHeaderRe.FindAllStringIndex(sec, -1)),
			body:         strings.TrimRight(sec, "\n") + "\n",
		})
	}
	return out, nil
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

// splitThreadSections splits the corpus body into thread sections on the `## ` boundary. It splits on
// the `\n## ` boundary (so a `#` inside a subject never false-splits) and keeps the `## ` prefix on
// each section.
func splitThreadSections(body string) []string {
	body = strings.TrimLeft(body, "\r\n")
	if body == "" {
		return nil
	}
	// Normalize so the first section (which starts at "## " with no preceding newline) is handled the
	// same as the rest: prepend a newline, then split on "\n## ".
	parts := strings.Split("\n"+body, "\n## ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		out = append(out, "## "+strings.TrimLeft(p, "\n"))
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
