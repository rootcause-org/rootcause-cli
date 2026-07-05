package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func newDreamCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "Inspect local dream-cycle evidence",
	}
	cmd.AddCommand(dreamEvidenceCmd(e))
	return cmd
}

func dreamEvidenceCmd(e *env) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "List feedback and sent-edit evidence for consolidation",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
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
	return cmd
}
