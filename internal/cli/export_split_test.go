package cli

import (
	"strings"
	"testing"
)

// A small fixed corpus (subjects containing `#` to exercise the boundary split), used by the splitter
// unit tests below.
const splitTestCorpus = `---
harvest_format: v1
mailbox: owner@x.com
harvested_at: 2026-07-06T10:00:00Z
threads: 2
cleaned: true
truncated: false
---

## Re: Invoice #42 question — #1
**Participants:** alice@acme.com, owner@x.com
**Span:** 2025-02-10 → 2025-02-18

**alice@acme.com (2025-02-18):**
Where is my invoice?
_[attachment: foo.pdf]_

**owner@x.com (2025-02-18):**
Attached now.

## Another subject — #2
**Participants:** bob@example.org, owner@x.com
**Span:** 2025-03-01 → 2025-03-02

**bob@example.org (2025-03-01):**
Thanks!
`

func TestParseCorpusSplitsThreads(t *testing.T) {
	c, err := parseCorpus(splitTestCorpus)
	if err != nil {
		t.Fatalf("parseCorpus: %v", err)
	}
	if c.mailbox != "owner@x.com" || c.harvestedAt != "2026-07-06T10:00:00Z" || c.cleaned != "true" {
		t.Errorf("front-matter = %+v", c)
	}
	if len(c.threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(c.threads))
	}

	// Subject containing `#` must not false-split, and the idx comes off the trailing — #<n>.
	t0 := c.threads[0]
	if t0.subject != "Re: Invoice #42 question" || t0.idx != 1 {
		t.Errorf("thread 0 subject/idx = %q/%d, want %q/1", t0.subject, t0.idx, "Re: Invoice #42 question")
	}
	if t0.spanStart != "2025-02-10" {
		t.Errorf("thread 0 span = %q, want 2025-02-10", t0.spanStart)
	}
	if t0.msgCount != 2 {
		t.Errorf("thread 0 msgCount = %d, want 2", t0.msgCount)
	}
	if strings.Join(t0.participants, ",") != "alice@acme.com,owner@x.com" {
		t.Errorf("thread 0 participants = %v", t0.participants)
	}
	if !strings.HasPrefix(t0.body, "## Re: Invoice #42 question — #1") {
		t.Errorf("thread 0 body should start at the header: %q", t0.body)
	}

	t1 := c.threads[1]
	if t1.idx != 2 || t1.spanStart != "2025-03-01" {
		t.Errorf("thread 1 idx/span = %d/%q", t1.idx, t1.spanStart)
	}
}

func TestThreadFileNameAndSlug(t *testing.T) {
	c, err := parseCorpus(splitTestCorpus)
	if err != nil {
		t.Fatalf("parseCorpus: %v", err)
	}
	got := threadFileName(c.threads[0], "2026-07")
	if got != "2025-02--re-invoice-42-question--1.md" {
		t.Errorf("file name = %q", got)
	}

	// A thread with no span falls back to the corpus harvested_at month.
	noSpan := c.threads[0]
	noSpan.spanStart = ""
	if got := threadFileName(noSpan, "2026-07"); got != "2026-07--re-invoice-42-question--1.md" {
		t.Errorf("fallback file name = %q", got)
	}

	// Slug caps length and collapses/trims non-alnum runs.
	if s := slugify("A Very  Long???  Subject Line That Exceeds The Forty Character Cap For Sure"); len(s) > 40 {
		t.Errorf("slug not capped: %q (%d)", s, len(s))
	}
	if s := slugify("!!!"); s != "thread" {
		t.Errorf("empty slug = %q, want thread", s)
	}
}

func TestParticipantDomains(t *testing.T) {
	got := participantDomains([]string{"alice@acme.com", "owner@x.com", "bob@ACME.com"})
	if strings.Join(got, ",") != "acme.com,x.com" {
		t.Errorf("domains = %v, want [acme.com x.com] (deduped, sorted, lowercased)", got)
	}
}

// TestParseCorpusRejectsVersionDrift is the load-bearing guard: a v2 (or missing) harvest_format must
// fail loudly so a future server render change can't be silently mis-parsed.
func TestParseCorpusRejectsVersionDrift(t *testing.T) {
	v2 := strings.Replace(splitTestCorpus, "harvest_format: v1", "harvest_format: v2", 1)
	if _, err := parseCorpus(v2); err == nil || !strings.Contains(err.Error(), "unsupported harvest_format") {
		t.Fatalf("expected an unsupported-version error for v2, got %v", err)
	}

	missing := strings.Replace(splitTestCorpus, "harvest_format: v1\n", "", 1)
	if _, err := parseCorpus(missing); err == nil || !strings.Contains(err.Error(), "missing harvest_format") {
		t.Fatalf("expected a missing-version error, got %v", err)
	}

	if _, err := parseCorpus("no front matter here"); err == nil {
		t.Fatal("expected an error for a corpus with no front-matter")
	}
}
