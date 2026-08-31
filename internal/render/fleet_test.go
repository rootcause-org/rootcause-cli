package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func runWithBash(id string, total, real, explore int64) client.RunSummary {
	return client.RunSummary{
		RunID:  id,
		Kind:   "analysis",
		Status: "done",
		Health: &client.RunHealth{
			BashErrCount:        total,
			BashErrRealCount:    real,
			BashErrExploreCount: explore,
		},
	}
}

// The split's whole point: explore noise never ranks. A run with only exploration failures must not
// appear in the bash-failure leaderboard, and a legacy row (total only, no split) counts as real.
func TestTopByBashErrRanksRealOnly(t *testing.T) {
	runs := []client.RunSummary{
		runWithBash("explore-only", 40, 0, 40),
		runWithBash("real-small", 15, 3, 12),
		runWithBash("legacy", 7, 0, 0), // pre-split server: total is real
		runWithBash("clean", 0, 0, 0),
	}
	got := topByBashErr(runs, 3)
	var ids []string
	for _, r := range got {
		ids = append(ids, r.RunID)
	}
	want := []string{"legacy", "real-small"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("topByBashErr = %v, want %v", ids, want)
	}
}

func TestBashErrTokenSplit(t *testing.T) {
	for _, tc := range []struct {
		real, explore int64
		want          string
	}{
		{3, 12, "ERR×3 (+12 explore)"},
		{0, 12, "ERR×0 (+12 explore)"},
		{3, 0, "ERR×3"},
	} {
		if got := bashErrToken(tc.real, tc.explore); got != tc.want {
			t.Errorf("bashErrToken(%d,%d) = %q, want %q", tc.real, tc.explore, got, tc.want)
		}
	}
}

// Explore-only noise must not earn a shortlist line, while real failures still weigh.
func TestSeverityIgnoresExploreNoise(t *testing.T) {
	if s := severity(runWithBash("explore-only", 40, 0, 40), nil); s != 0 {
		t.Fatalf("severity(explore-only) = %d, want 0", s)
	}
	if s := severity(runWithBash("legacy", 7, 0, 0), nil); s != 70 {
		t.Fatalf("severity(legacy) = %d, want 70 (fallback treats total as real)", s)
	}
}

func TestShadowLearningLabelsAndSeverity(t *testing.T) {
	for _, tc := range []struct {
		verdict string
		label   string
		severe  bool
	}{
		{"equivalent", "sent_delta/equivalent", false},
		{"same_outcome_details_differ", "sent_delta/same_outcome_details_differ", false},
		{"divergent_facts", "sent_delta/divergent_facts", true},
		{"missed_content", "sent_delta/missed_content", true},
		{"not_answerable", "sent_delta/not_answerable", false},
		{"", "sent_delta/unjudged", false},
	} {
		learning := client.Learning{SentDelta: true, SentDeltaShadow: true, SentDeltaVerdict: tc.verdict}
		if got := fleetLearningLabel(learning); got != tc.label {
			t.Errorf("fleetLearningLabel(%q) = %q, want %q", tc.verdict, got, tc.label)
		}
		run := client.RunSummary{RunID: tc.verdict, Status: "done", Learning: learning}
		if got := severity(run, nil); (got > 0) != tc.severe {
			t.Errorf("severity(%q) = %d, severe=%t", tc.verdict, got, tc.severe)
		}
	}
	if got := fleetLearningLabel(client.Learning{SentDelta: true}); got != "sent_delta" {
		t.Errorf("live delta label = %q, want sent_delta", got)
	}
}

// The per-project rollup's BASH_ERR column carries the same split.
func TestFleetTotalBashErrColumnSplits(t *testing.T) {
	var buf bytes.Buffer
	fleetTotal(&buf, []FleetGroup{{
		Project: "acme",
		Runs:    []client.RunSummary{runWithBash("a", 15, 3, 12), runWithBash("b", 40, 0, 40)},
	}})
	if !strings.Contains(buf.String(), "3 (+52 explore)") {
		t.Fatalf("rollup missing split BASH_ERR cell:\n%s", buf.String())
	}
}

// Turn spikes replace the old cost spike: >3× the same-kind median, and only once a kind has ≥4 runs.
func TestTurnSpikesFlagOutliers(t *testing.T) {
	run := func(id string, turns int64) client.RunSummary {
		return client.RunSummary{RunID: id, Kind: "analysis", Status: "done", Health: &client.RunHealth{Turns: turns}}
	}
	runs := []client.RunSummary{run("a", 4), run("b", 5), run("c", 4), run("heavy", 40)}
	spikes := turnSpikes(runs)
	if !spikes["heavy"] {
		t.Fatalf("expected heavy to spike, got %v", spikes)
	}
	if len(spikes) != 1 {
		t.Fatalf("expected exactly one spike, got %v", spikes)
	}
	if got := flagStr(run("heavy", 40), spikes); !strings.Contains(got, "T!") {
		t.Fatalf("flags = %q, want T!", got)
	}
	// Too few runs of a kind ⇒ no baseline, no spike.
	if s := turnSpikes(runs[:3]); len(s) != 0 {
		t.Fatalf("expected no spikes below the 4-run baseline, got %v", s)
	}

	// Zero-turn rows (still running, died before turn 1, no health block) must not join the sample:
	// counting them as 0 would halve the median and flag an ordinary run.
	noHealth := client.RunSummary{RunID: "nh", Kind: "analysis", Status: "running"}
	mixed := []client.RunSummary{run("a", 4), run("b", 5), run("c", 4), run("d", 6), run("z", 0), noHealth}
	if s := turnSpikes(mixed); len(s) != 0 {
		t.Fatalf("zero-turn rows must not pull the median down, got %v", s)
	}
	// …and they must not starve a kind of its baseline either: 4 turned runs still spike.
	if s := turnSpikes(append(mixed, run("heavy", 40))); !s["heavy"] || len(s) != 1 {
		t.Fatalf("expected only heavy to spike, got %v", s)
	}
}
