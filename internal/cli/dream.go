package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func dreamEvidenceCmd(e *env) *cobra.Command {
	var limit int
	var days int
	var plane string
	var shadow bool
	var verdicts []string
	var includeBodies bool
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "List feedback, sent-delta, shadow, and triage evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("days") && days <= 0 {
				return fmt.Errorf("--days must be greater than zero")
			}
			if err := validateDreamPlane(plane); err != nil {
				return err
			}
			if err := validateShadowVerdicts(verdicts); err != nil {
				return err
			}
			shadowSet := cmd.Flags().Changed("shadow")
			if plane == "shadow" {
				if shadowSet && !shadow {
					return fmt.Errorf("--plane shadow conflicts with --shadow=false")
				}
				plane = "deltas"
				shadow = true
				shadowSet = true
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.DreamEvidence(e.ctx(), client.DreamEvidenceParams{
				Project: e.scopeProject(), Tenant: e.scopeTenant(), Limit: limit, Days: days,
				Plane: plane, Shadow: shadow, ShadowSet: shadowSet, Verdicts: verdicts,
				IncludeBodies: includeBodies,
			})
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows per evidence plane (server default 20, cap 100)")
	cmd.Flags().IntVar(&days, "days", 0, "only evidence from the last N days")
	cmd.Flags().StringVar(&plane, "plane", "", "evidence plane: feedback|deltas|shadow|triage (default all; shadow aliases deltas --shadow)")
	cmd.Flags().BoolVar(&shadow, "shadow", false, "filter sent deltas by shadow mode (use --shadow=false for live rows)")
	cmd.Flags().StringSliceVar(&verdicts, "verdict", nil, "filter shadow rows by verdict (comma-separated or repeatable)")
	cmd.Flags().BoolVar(&includeBodies, "include-bodies", false, "include proposed and sent bodies in delta evidence")
	return cmd
}

func validateDreamPlane(plane string) error {
	switch plane {
	case "", "feedback", "deltas", "shadow", "triage":
		return nil
	default:
		return fmt.Errorf("invalid --plane %q (want feedback, deltas, shadow, or triage)", plane)
	}
}

func validateShadowVerdicts(verdicts []string) error {
	for _, verdict := range verdicts {
		switch verdict {
		case "equivalent", "same_outcome_details_differ", "divergent_facts", "missed_content", "not_answerable":
		default:
			return fmt.Errorf("invalid --verdict %q", verdict)
		}
	}
	return nil
}
