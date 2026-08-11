// `rc fleet deploy-state` — the audit pre-flight: which host build, which brain channel pointer, and
// which source-mirror commits are LIVE right now, plus the timelines the server stores. Run it before
// attributing a defect, so a finding is never blamed on code or knowledge that was not yet deployed.
//
// The host plane has no server-side history (the box only knows what it runs now). --host-repo closes
// that gap locally and offline: it lists the commits on a rootcause checkout's main that the deployed
// release does not contain. It never fetches — the caller decides how fresh the checkout is.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// maxUnpromotedCommits caps the local delta list so a badly-stale checkout cannot flood the report.
const maxUnpromotedCommits = 40

func newDeployStateCmd(e *env) *cobra.Command {
	var history int
	var hostRepo string
	cmd := &cobra.Command{
		Use:   "deploy-state",
		Short: "Show the live host, brain, and mirror SHAs with their history",
		Long: "Fetch GET /api/v1/deploy-state and render what is LIVE per plane: the host release the " +
			"server was built from, the project brain's channel pointers (plus the recorded promotion " +
			"timeline), and each source mirror's checked-out commit (plus its refresh timeline). " +
			"--host-repo <path> additionally lists the commits on that local rootcause checkout's main " +
			"that the deployed release does not contain (offline; it never fetches). -o json passes the " +
			"raw server rows through, with the local delta added under \"unpromoted\".",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Raw(e.ctx(), "GET", client.DeployStatePath(history, e.scopeProject(), e.scopeTenant()), nil)
			if err != nil {
				return err
			}
			var resp client.DeployStateResponse
			if uerr := json.Unmarshal(raw, &resp); uerr != nil {
				return fmt.Errorf("decode deploy-state response: %w", uerr)
			}

			var unpromoted *render.HostUnpromoted
			if hostRepo != "" {
				unpromoted = localUnpromoted(hostRepo, resp.Host.Release)
			}

			if e.jsonOut() {
				return e.renderJSON("deploy-state", withUnpromoted(raw, unpromoted))
			}
			render.DeployState(e.out, &resp, unpromoted)
			return nil
		},
	}
	cmd.Flags().IntVar(&history, "history", 15, "how many brain promotions and mirror refreshes to list")
	cmd.Flags().StringVar(&hostRepo, "host-repo", "", "path to a local rootcause checkout, to list commits not in the deployed release")
	return cmd
}

// withUnpromoted splices the locally-derived delta into the raw server JSON so `-o json | jq` sees one
// object. A marshal failure falls back to the untouched server bytes — the raw rows are the contract.
func withUnpromoted(raw json.RawMessage, u *render.HostUnpromoted) json.RawMessage {
	if u == nil {
		return raw
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	payload, err := json.Marshal(map[string]any{
		"repo": u.Repo, "ref": u.Ref, "release": u.Release,
		"commits": u.Commits, "count": len(u.Commits), "error": u.Error,
	})
	if err != nil {
		return raw
	}
	obj["unpromoted"] = payload
	merged, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return merged
}

// localUnpromoted lists the commits a local rootcause checkout has on main (origin/main when present —
// the shared truth — else the local main) that the deployed release does not contain. Every failure is
// reported as an Error rather than an empty list: "no local checkout" must never read as "nothing
// pending". The release SHA is passed to git only after a hex check, so it can't smuggle a git option.
func localUnpromoted(repoPath, release string) *render.HostUnpromoted {
	out := &render.HostUnpromoted{Repo: repoPath, Release: release}
	if release == "" {
		out.Error = "the server reported no release SHA"
		return out
	}
	if !isHexSHA(release) {
		out.Error = "the server reported a non-SHA release: " + release
		return out
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Repo = abs
	if fi, serr := os.Stat(filepath.Join(abs, ".git")); serr != nil || (!fi.IsDir() && fi.Size() == 0) {
		out.Error = abs + " is not a git checkout"
		return out
	}
	if _, gerr := gitOut(abs, "cat-file", "-e", release+"^{commit}"); gerr != nil {
		out.Error = "release " + release + " is not in " + abs + " (fetch it first)"
		return out
	}

	ref := "origin/main"
	if _, gerr := gitOut(abs, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/main"); gerr != nil {
		ref = "main"
	}
	out.Ref = ref
	lines, gerr := gitOut(abs, "log", "--oneline", "--no-decorate", fmt.Sprintf("--max-count=%d", maxUnpromotedCommits+1), release+".."+ref)
	if gerr != nil {
		out.Error = "git log " + release + ".." + ref + " failed: " + gerr.Error()
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		if line != "" {
			out.Commits = append(out.Commits, line)
		}
	}
	if len(out.Commits) > maxUnpromotedCommits {
		out.Commits = append(out.Commits[:maxUnpromotedCommits], fmt.Sprintf("… more than %d commits behind", maxUnpromotedCommits))
	}
	return out
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
