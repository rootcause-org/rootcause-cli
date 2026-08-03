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
	if s := severity(runWithBash("explore-only", 40, 0, 40), nil, 0); s != 0 {
		t.Fatalf("severity(explore-only) = %d, want 0", s)
	}
	if s := severity(runWithBash("legacy", 7, 0, 0), nil, 0); s != 70 {
		t.Fatalf("severity(legacy) = %d, want 70 (fallback treats total as real)", s)
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
