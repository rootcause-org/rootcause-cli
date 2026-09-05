// The human `diagnostics.md` written next to a split harvest corpus — why candidates were rejected,
// which actors/initiations were seen, and how the candidate window was banded.

package render

import (
	"fmt"
	"sort"
	"strings"
)

// HarvestBand is one candidate time band with its count.
type HarvestBand struct {
	Start string
	End   string
	Count int
}

// HarvestDiagnostics is the split corpus's diagnostics block, as rendered.
type HarvestDiagnostics struct {
	AcceptedCount    int
	RejectionReasons map[string]int
	ActorTypes       map[string]int
	InitiationTypes  map[string]int
	CandidateBands   []HarvestBand
	Deduplicated     int
}

// SplitDiagnostics renders the diagnostics markdown document. It returns a string because the caller
// writes it to a file in the export directory, not to stdout.
func SplitDiagnostics(d HarvestDiagnostics) string {
	var b strings.Builder
	b.WriteString("# Harvest diagnostics\n\n")
	fmt.Fprintf(&b, "Accepted: %d; rejected: %d; deduplicated candidates: %d.\n", d.AcceptedCount, sumCounts(d.RejectionReasons), d.Deduplicated)
	writeCountBlock(&b, "Rejection reasons", d.RejectionReasons)
	writeCountBlock(&b, "Actor types", d.ActorTypes)
	writeCountBlock(&b, "Initiation types", d.InitiationTypes)
	if len(d.CandidateBands) > 0 {
		b.WriteString("\nCandidate bands:\n")
		for _, band := range d.CandidateBands {
			fmt.Fprintf(&b, "- %s..%s: %d\n", band.Start, band.End, band.Count)
		}
	}
	return b.String()
}

func writeCountBlock(b *strings.Builder, title string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, key := range keys {
		fmt.Fprintf(b, "- %s: %d\n", key, counts[key])
	}
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
