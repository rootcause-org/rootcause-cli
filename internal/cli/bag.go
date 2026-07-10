package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// This file factors the config-bag command shape shared by project runtime settings, knowledge sync,
// branding, and action settings. Every bag is GET/PATCH /api/v1/<bag> returning the same generic
// {key:{value,effective,default,source}} map, so ONE pair of get/set builders — parameterized by the
// bag's base path — serves them all. The server owns the whitelist + validation; the CLI shapes the
// sparse PATCH (schema-aware value coercion) and renders the result, JSON passthrough included.

// bagPath builds the request path for a bag GET/PATCH, appending ?project= when an all-projects token is
// targeting a specific project (mirrors client.bagURL for the raw JSON-passthrough path).
func bagPath(base, project string) string {
	if project == "" {
		return base
	}
	return base + "?project=" + url.QueryEscape(project)
}

// newBagGetCmd builds the `get` subcommand for the bag at base (e.g. "/api/v1/kb").
func newBagGetCmd(e *env, base string) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show current values (value / effective / default)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "GET", bagPath(base, e.scopeProject()), nil)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			s, err := c.GetBag(e.ctx(), base, e.scopeProject())
			if err != nil {
				return err
			}
			render.Settings(e.out, s)
			return nil
		},
	}
}

// newBagSetCmd builds the `set k=v …` subcommand for the bag at base. Value coercion is schema-aware
// (one /meta/schema fetch classifies each key by its declared type), so a bool/number/list field rides
// as the right JSON shape; the server is the final validator.
func newBagSetCmd(e *env, base string) *cobra.Command {
	return &cobra.Command{
		Use:   "set k=v [k=v…]",
		Short: "Change values (sparse, validate-then-apply server-side)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			patch, err := parseSetArgs(args, newValueCoercer(e, c))
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				raw, err := c.Raw(e.ctx(), "PATCH", bagPath(base, e.scopeProject()), patch)
				if err != nil {
					return err
				}
				return render.JSON(e.out, raw)
			}
			s, err := c.PatchBag(e.ctx(), base, patch, e.scopeProject())
			if err != nil {
				return err
			}
			render.Settings(e.out, s)
			return nil
		},
	}
}

// newBrandingCmd builds `rc branding get|set` over GET/PATCH /api/v1/branding — white-label appearance
// + public base URL. (The logo binary is its own endpoint, not part of this bag.)
func newBrandingCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "branding", Short: "Read or change white-label branding (colours/name/public_base_url)"}
	cmd.AddCommand(newBagGetCmd(e, "/api/v1/branding"), newBagSetCmd(e, "/api/v1/branding"), newBrandingLogoCmd(e))
	return cmd
}

// newBrandingLogoCmd builds `rc branding logo set <file>|clear` over the binary logo endpoint (PUT
// multipart / DELETE /api/v1/branding/logo) — separate from the branding bag, which carries only the
// scalar appearance keys.
func newBrandingLogoCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "logo", Short: "Set or clear the white-label logo image"}
	cmd.AddCommand(brandingLogoSetCmd(e), brandingLogoClearCmd(e))
	return cmd
}

func brandingLogoSetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "set <file>",
		Short: "Upload a logo image (PNG/SVG/JPEG)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			ct := logoContentType(path, data)
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.SetBrandingLogo(e.ctx(), filepath.Base(path), ct, data, e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() && len(raw) > 0 {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintf(e.out, "uploaded logo %s (%s, %d bytes)\n", filepath.Base(path), ct, len(data))
			return nil
		},
	}
}

func brandingLogoClearCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove the stored logo",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.ClearBrandingLogo(e.ctx(), e.scopeProject())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return render.JSON(e.out, raw)
				}
				return render.JSON(e.out, []byte(`{"cleared":true}`))
			}
			_, _ = fmt.Fprintln(e.out, "logo cleared")
			return nil
		},
	}
}

// logoContentType resolves the upload MIME from the file extension, sniffing the bytes only when the
// extension is unknown — SVG sniffs as text/xml or text/plain, so the extension wins for it.
func logoContentType(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return http.DetectContentType(data)
	}
}

// newActionConfigCmd builds `rc project action-settings get|set` over GET/PATCH /api/v1/action — the
// operator-tier action-plane wiring (enabled/mode/runner_url/result_url + the write-only reverse secret).
func newActionConfigCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "action-settings", Short: "Read or change action-plane config (operator-tier)"}
	cmd.AddCommand(newBagGetCmd(e, "/api/v1/action"), newBagSetCmd(e, "/api/v1/action"))
	return cmd
}
