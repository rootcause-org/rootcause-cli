package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func dreamEvidenceCmd(e *env) *cobra.Command {
	var limit int
	var plane string
	var includeBodies bool
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "List feedback, sent-edit, and triage evidence for consolidation",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := validateDreamPlane(plane); err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			q := url.Values{}
			if project := e.scopeProject(); project != "" {
				q.Set("project", project)
			}
			if tenant := e.scopeTenant(); tenant != "" {
				q.Set("tenant", tenant)
			}
			if limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", limit))
			}
			if plane != "" {
				q.Set("plane", plane)
			}
			if includeBodies {
				q.Set("include_bodies", "true")
			}
			path := "/api/v1/dream/evidence"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			raw, err := c.Raw(e.ctx(), http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			return render.JSON(e.out, raw)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max rows per evidence plane (server default 20, cap 100)")
	cmd.Flags().StringVar(&plane, "plane", "", "evidence plane: feedback|deltas|triage (default all)")
	cmd.Flags().BoolVar(&includeBodies, "include-bodies", false, "include proposed and sent bodies in delta evidence")
	return cmd
}

func validateDreamPlane(plane string) error {
	switch plane {
	case "", "feedback", "deltas", "triage":
		return nil
	default:
		return fmt.Errorf("invalid --plane %q (want feedback, deltas, or triage)", plane)
	}
}
