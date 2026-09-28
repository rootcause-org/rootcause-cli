package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// newSettingProposalsCmd wires `rc project settings proposals` — the review queue for learned setting
// changes (…/settings-proposals). --tenant narrows to that tenant's own proposals; without it the
// project reviewer sees every tier.
func newSettingProposalsCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "proposals", Short: "Review proposed settings changes (list, approve, reject)"}
	cmd.AddCommand(proposalsListCmd(e), proposalsShowCmd(e), proposalsDecideCmd(e, "approve"), proposalsDecideCmd(e, "reject"))
	return cmd
}

func proposalsListCmd(e *env) *cobra.Command {
	var pending bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List settings proposals (pending first, then recently decided)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, project, err := proposalsClient(e)
			if err != nil {
				return err
			}
			items, raw, err := c.SettingProposals(e.ctx(), project, e.scopeTenant())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			if pending {
				kept := items[:0]
				for _, p := range items {
					if p.Status == "proposed" {
						kept = append(kept, p)
					}
				}
				items = kept
			}
			render.SettingProposals(e.out, items)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pending, "pending", false, "show only proposals still awaiting a decision (table only)")
	return cmd
}

func proposalsShowCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one settings proposal with its payload and evidence",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, project, err := proposalsClient(e)
			if err != nil {
				return err
			}
			id, err := resolveProposalID(e, c, project, args[0])
			if err != nil {
				return err
			}
			raw, err := c.SettingProposalGet(e.ctx(), project, e.scopeTenant(), id)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
}

// proposalsDecideCmd builds approve|reject. A failed apply is recorded server-side and answered 200; the
// CLI turns it into a non-zero exit so scripts can't mistake it for success.
func proposalsDecideCmd(e *env, verb string) *cobra.Command {
	var reason string
	short := "Approve and apply a settings proposal"
	if verb == "reject" {
		short = "Reject a pending settings proposal"
	}
	cmd := &cobra.Command{
		Use:   verb + " <full-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, project, err := proposalsClient(e)
			if err != nil {
				return err
			}
			id, err := fullProposalID(args[0])
			if err != nil {
				return err
			}
			rec, raw, err := c.SettingProposalDecide(e.ctx(), project, e.scopeTenant(), id, verb, reason)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if err := render.JSON(e.out, raw); err != nil {
					return err
				}
			} else {
				_, _ = fmt.Fprintf(e.out, "%s settings proposal %s: %s\n", rec.Status, rec.ID, render.ProposalChange(*rec))
			}
			if rec.Status == "failed" {
				return fmt.Errorf("apply failed: %s", rec.Error)
			}
			return nil
		},
	}
	if verb == "reject" {
		cmd.Flags().StringVar(&reason, "reason", "", "why (recorded in the decision audit)")
	}
	return cmd
}

func proposalsClient(e *env) (*client.Client, string, error) {
	c, err := e.newClient()
	if err != nil {
		return nil, "", err
	}
	project, err := pathProject(e, c, "settings proposals")
	if err != nil {
		return nil, "", err
	}
	return c, project, nil
}

// fullProposalID gates approve/reject: the list only windows the decided tail, so a prefix unique within
// it can still name a different (older) proposal than the one the caller means.
func fullProposalID(arg string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(arg))
	if !fullUUID.MatchString(id) {
		return "", fmt.Errorf("proposal id %q: approve/reject need the full proposal id (see the ID column of `rc project settings proposals ls`, or `show <prefix>`)", arg)
	}
	return id, nil
}

var fullUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// resolveProposalID (show only) accepts the full uuid or a prefix unique among the listed rows.
func resolveProposalID(e *env, c *client.Client, project, arg string) (string, error) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if len(arg) == 36 {
		return arg, nil
	}
	if len(arg) < 8 {
		return "", fmt.Errorf("proposal id %q: give the full uuid or at least 8 characters", arg)
	}
	items, _, err := c.SettingProposals(e.ctx(), project, e.scopeTenant())
	if err != nil {
		return "", err
	}
	var match string
	for _, p := range items {
		if strings.HasPrefix(p.ID, arg) {
			if match != "" {
				return "", fmt.Errorf("proposal id %q is ambiguous; give more characters", arg)
			}
			match = p.ID
		}
	}
	if match == "" {
		return "", fmt.Errorf("no settings proposal matches %q", arg)
	}
	return match, nil
}
