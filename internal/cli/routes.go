package cli

import (
	"github.com/spf13/cobra"
)

func newRoutesCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "routes",
		Short: "Show the canonical API route manifest",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			manifest, raw, err := c.Routes(e.ctx())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("routes", raw)
			}
			for _, r := range manifest.Routes {
				dep := ""
				if r.Deprecated {
					dep = " deprecated"
				}
				_, _ = e.out.Write([]byte(r.Method + " " + r.Path + dep + "\n"))
			}
			return nil
		},
	}
}

func newOpenAPICmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "openapi",
		Short: "Dump the canonical OpenAPI document",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.OpenAPI(e.ctx())
			if err != nil {
				return err
			}
			return e.renderJSON("openapi", raw)
		},
	}
}
