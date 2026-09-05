// The FAT side of `rc fleet deploy-state`: a compact, agent-readable "what was live" report over the
// raw /api/v1/deploy-state rows. Deliberately verdict-free — deploy state is context for a finding, not
// a pass/fail gate (that is `rc fleet health`). Timestamps are printed exactly as the server sent them
// (RFC3339 UTC), so an audit can compare them to a run's created_at without timezone arithmetic.
package render

import (
	"fmt"
	"io"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// HostUnpromoted is the locally-derived host-plane history the server cannot know: the commits on a
// local rootcause checkout's main that are NOT in the deployed release. Repo/Ref name where it came
// from; Error explains an unusable checkout instead of silently reporting "nothing pending".
type HostUnpromoted struct {
	Repo    string
	Ref     string
	Release string
	Commits []string
	Error   string
}

// DeployState renders the three planes: host build, brain channels + promotions, mirrors + refresh
// timeline. unpromoted is nil when no local checkout was pointed at.
func DeployState(w io.Writer, d *client.DeployStateResponse, unpromoted *HostUnpromoted) {
	_, _ = fmt.Fprintf(w, "Deploy state — %s @ %s (UTC)\n", d.Project, d.GeneratedAt)

	_, _ = fmt.Fprintf(w, "\nHost (rootcause) — release %s\n", orDash(d.Host.Release, "-"))
	_, _ = fmt.Fprintf(w, "  up since %s (%.1fh)\n", orDash(d.Host.StartedAt, "-"), d.Host.UptimeHours)
	renderUnpromoted(w, unpromoted)

	_, _ = fmt.Fprintf(w, "\nBrain (%s) — local main %s · state %s\n", d.Project, orDash(clipID(d.Brain.MainSHA, 12), "-"), orDash(d.Brain.State, "-"))
	if len(d.Brain.Channels) == 0 {
		_, _ = fmt.Fprintln(w, "  (no channel pointers on the box)")
	}
	for _, c := range d.Brain.Channels {
		_, _ = fmt.Fprintf(w, "  %-7s %s  state=%s origin=%s main=%s\n",
			c.Channel, orDash(clipID(c.ResolvedSHA, 12), "-"), c.State, yesNo(c.MatchesOrigin), yesNo(c.MatchesMain))
	}
	if len(d.Brain.Promotions) > 0 {
		_, _ = fmt.Fprintf(w, "  promotions (last %d):\n", len(d.Brain.Promotions))
		for _, p := range d.Brain.Promotions {
			when := p.FinishedAt
			if when == "" {
				when = p.CreatedAt
			}
			_, _ = fmt.Fprintf(w, "    %s  %-7s %s → %s  %s  %s\n",
				when, p.Channel, orDash(clipID(p.OldSHA, 12), "-"), orDash(clipID(promotedSHA(p), 12), "-"), p.Outcome, p.Actor)
		}
	} else {
		_, _ = fmt.Fprintln(w, "  promotions: none recorded")
	}

	_, _ = fmt.Fprintf(w, "\nMirrors — %d\n", len(d.Mirrors))
	for _, m := range d.Mirrors {
		_, _ = fmt.Fprintf(w, "  %-28s %s  state=%s last_ok=%s  %s\n",
			mirrorName(m.Repo, m.Tenant), orDash(clipID(m.SHA, 12), "-"), m.State, orDash(m.LastOkAt, "-"), clipStr(m.Subject, 60))
	}
	if len(d.MirrorHistory) > 0 {
		_, _ = fmt.Fprintf(w, "  refreshes (last %d, newest first):\n", len(d.MirrorHistory))
		for _, e := range d.MirrorHistory {
			_, _ = fmt.Fprintf(w, "    %s  %-28s %s  %s\n",
				e.RefreshedAt, mirrorName(e.Repo, e.Tenant), orDash(clipID(e.SHA, 12), "-"), clipStr(e.Subject, 50))
		}
	} else {
		_, _ = fmt.Fprintln(w, "  refreshes: none recorded yet")
	}

	_, _ = fmt.Fprintln(w, "\nnote: host deploy history is not stored server-side — see `gh run list --workflow deploy.yml`.")
}

// renderUnpromoted prints the local-checkout delta, or the reason it is unavailable — never silence,
// which would read as "everything is deployed".
func renderUnpromoted(w io.Writer, u *HostUnpromoted) {
	switch {
	case u == nil:
		_, _ = fmt.Fprintln(w, "  unpromoted: not checked (pass --host-repo <path to rootcause checkout>)")
	case u.Error != "":
		_, _ = fmt.Fprintf(w, "  unpromoted: unavailable — %s\n", u.Error)
	case len(u.Commits) == 0:
		_, _ = fmt.Fprintf(w, "  unpromoted: none — %s matches the deployed release\n", u.Ref)
	default:
		_, _ = fmt.Fprintf(w, "  unpromoted: %d commit(s) on %s not in release %s\n", len(u.Commits), u.Ref, u.Release)
		for _, c := range u.Commits {
			_, _ = fmt.Fprintf(w, "    %s\n", c)
		}
	}
}

// promotedSHA is the SHA a promotion actually moved the channel to; an unfinished attempt has none, so
// fall back to what was requested (rendered alongside its "started" outcome).
func promotedSHA(p client.DeployBrainPromotion) string {
	if p.NewSHA != "" {
		return p.NewSHA
	}
	return p.RequestedSHA
}

func mirrorName(repo, tenant string) string {
	if tenant == "" {
		return repo
	}
	return repo + " [" + tenant + "]"
}
