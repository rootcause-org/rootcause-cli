package cli

import (
	"encoding/json"
	"net/http"

	"github.com/spf13/cobra"
)

type routeManifest struct {
	Routes []apiRoute `json:"routes"`
}

type apiRoute struct {
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Summary    string   `json:"summary"`
	Auth       string   `json:"auth"`
	Scopes     []string `json:"scopes,omitempty"`
	Request    string   `json:"request,omitempty"`
	Response   string   `json:"response,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
}

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
			raw, err := c.Raw(e.ctx(), http.MethodGet, "/api/v1/meta/routes", nil)
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("routes", raw)
			}
			var manifest routeManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				return err
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
			raw, err := c.Raw(e.ctx(), http.MethodGet, "/api/v1/meta/openapi.json", nil)
			if err != nil {
				return err
			}
			return e.renderJSON("openapi", raw)
		},
	}
}
