package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func collectionTenant(e *env, resource string) string {
	switch resource {
	case "connections", "repos", "members":
		return e.scopeTenant()
	default:
		return ""
	}
}

// asItem decodes a flat JSON object into a client.Item for the generic key:value renderer. A non-object
// body yields an empty item (the renderer prints "(no fields returned)"), so a bespoke endpoint that
// returns a small object renders without a dedicated wire struct.
func asItem(raw []byte) client.Item {
	var it client.Item
	_ = json.Unmarshal(raw, &it)
	return it
}

// This file wires the collection noun commands (repo / connection / member / token) onto the generic
// collections client + flat-item renderers. Each command is a thin adapter — parse flags → one client
// call → render — and holds NO per-resource field knowledge: an item is whatever flat keys the server
// returned. The verb grammar follows the server: ls/add/set/rm plus the connection/token item-verbs
// (reveal/rotate/revoke/mint). Mirrors console.go's command-group structure.

// listSubCmd is the shared `ls` verb over GET /api/v1/<resource>.
func listSubCmd(e *env, resource string) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List " + resource,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			l, raw, err := c.List(e.ctx(), resource, e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON(resource+"-ls", raw)
			}
			render.ItemList(e.out, l)
			return nil
		},
	}
}

// addSubCmd is the shared `add` verb over POST /api/v1/<resource>, taking k=v field args. List-typed
// fields aren't auto-detected here (no per-resource schema wired for collections) — a value with commas
// is sent as a string; use repeated keys server-side if a list field is needed. The server validates.
func addSubCmd(e *env, resource string) *cobra.Command {
	return &cobra.Command{
		Use:   "add k=v [k=v…]",
		Short: "Create a " + collectionSingular(resource),
		Args:  cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Create(e.ctx(), resource, body, e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON(resource+"-add", raw)
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

// getSubCmd is the shared `get <id>` verb over GET /api/v1/<resource>/<id> — one element as a
// key:value block (or -o json passthrough).
func getSubCmd(e *env, resource, idHelp string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <" + idHelp + ">",
		Short: "Show one " + resource[:len(resource)-1],
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Get(e.ctx(), resource, args[0], e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON(resource+"-get-"+args[0], raw)
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

// setSubCmd is the `set <id> k=v…` verb over PATCH /api/v1/<resource>/<id> (sparse update).
func setSubCmd(e *env, resource, idHelp string) *cobra.Command {
	return &cobra.Command{
		Use:   "set <" + idHelp + "> k=v [k=v…]",
		Short: "Update a " + collectionSingular(resource),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args[1:])
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Patch(e.ctx(), resource, args[0], body, e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON(resource+"-set-"+args[0], raw)
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

// rmSubCmd is the shared `rm <id>` verb over DELETE /api/v1/<resource>/<id>. A 2xx with no body prints a
// terse confirmation; any returned body rides through -o json verbatim.
func rmSubCmd(e *env, resource, idHelp string) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <" + idHelp + ">",
		Short: "Delete a " + collectionSingular(resource),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Delete(e.ctx(), resource, args[0], e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return e.renderJSON(resource+"-rm-"+args[0], raw)
				}
				return e.renderJSON(resource+"-rm-"+args[0], []byte(`{"deleted":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "deleted %s %s\n", resource, args[0])
			return nil
		},
	}
}

func collectionSingular(resource string) string {
	if resource == "databases" {
		return "database"
	}
	return strings.TrimSuffix(resource, "s")
}

// verbSubCmd is a no-body item-verb (rotate/revoke) over POST /api/v1/<resource>/<id>/<verb>.
func verbSubCmd(e *env, resource, idHelp, verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <" + idHelp + ">",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Verb(e.ctx(), resource, args[0], verb, e.scopeProject(), collectionTenant(e, resource))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON(resource+"-"+verb+"-"+args[0], raw)
			}
			if len(item) == 0 {
				_, _ = fmt.Fprintf(e.out, "%s %s: %s ok\n", resource, args[0], verb)
				return nil
			}
			render.Item(e.out, item)
			return nil
		},
	}
}

// newRepoCmd: `rc repo ls/add/set/rm` over the repos collection (id = repo name).
func newRepoCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Manage source repos (mirrors + per-repo PR config)"}
	cmd.AddCommand(
		listSubCmd(e, "repos"),
		addSubCmd(e, "repos"),
		setSubCmd(e, "repos", "name"),
		rmSubCmd(e, "repos", "name"),
	)
	return cmd
}

// newConnectionCmd: `rc connection ls/add/reveal/rotate/rm` over the connections collection (id = uuid).
// reveal prints the connection's secret to stdout ONCE — a sensitive, audited read.
func newConnectionCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "connection", Short: "Manage outbound integration connections"}
	cmd.AddCommand(
		listSubCmd(e, "connections"),
		addSubCmd(e, "connections"),
		connectionProbeCmd(e),
		connectionRevealCmd(e),
		verbSubCmd(e, "connections", "id", "rotate", "Rotate a connection's secret"),
		connectionRmCmd(e),
	)
	return cmd
}

func connectionProbeCmd(e *env) *cobra.Command {
	var write bool
	var notionPage string
	var cleanup bool
	var label string
	cmd := &cobra.Command{
		Use:   "probe <capability>",
		Short: "Probe an integration capability grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			res, raw, err := c.ConnectionProbe(e.ctx(), client.ConnectionProbeRequest{
				Capability: args[0],
				Label:      label,
				Write:      write,
				NotionPage: notionPage,
				Cleanup:    cleanup,
			}, e.scopeProject(), collectionTenant(e, "connections"))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return e.renderJSON("connection-probe-"+args[0], raw)
			}
			render.ConnectionProbe(e.out, res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "perform the provider write/read-back probe")
	cmd.Flags().StringVar(&notionPage, "notion-page", "", "Notion page id for notion.write --write")
	cmd.Flags().BoolVar(&cleanup, "cleanup", false, "delete/archive the probe artifact when supported")
	cmd.Flags().StringVar(&label, "label", "", "OAuth grant label (default account when empty)")
	return cmd
}

// connectionRevealCmd: POST /api/v1/connections/{id}/reveal → {"secret":"…"}. The secret is printed
// raw to stdout (so it can be captured) with a stderr warning that it's sensitive and shown once.
func connectionRevealCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "reveal <id>",
		Short: "Print a connection's secret (sensitive, shown once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Verb(e.ctx(), "connections", args[0], "reveal", e.scopeProject(), collectionTenant(e, "connections"))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.err, "warning: this is a live secret — handle with care; it is shown once")
			render.Secret(e.out, item)
			return nil
		},
	}
}

// connectionRmCmd revokes then deletes: the contract exposes both /revoke and DELETE. `rm` issues the
// revoke (so the secret stops working immediately) and then deletes the row.
func connectionRmCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Revoke and delete a connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if _, _, err := c.Verb(e.ctx(), "connections", args[0], "revoke", e.scopeProject(), collectionTenant(e, "connections")); err != nil {
				return err
			}
			raw, err := c.Delete(e.ctx(), "connections", args[0], e.scopeProject(), collectionTenant(e, "connections"))
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return e.renderJSON("connections-rm-"+args[0], raw)
				}
				return e.renderJSON("connections-rm-"+args[0], []byte(`{"revoked":"`+args[0]+`","deleted":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "revoked and deleted connection %s\n", args[0])
			return nil
		},
	}
}

// newMemberCmd: `rc member ls/add/rm` over the members collection (no read/update server-side → 405).
func newMemberCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "member", Short: "Manage project members"}
	cmd.AddCommand(
		listSubCmd(e, "members"),
		addSubCmd(e, "members"),
		rmSubCmd(e, "members", "id"),
	)
	return cmd
}

// newTokenCmd: `rc token ls/mint/revoke` over the tokens collection. mint (POST) returns the refresh
// token ONCE — printed plainly with a sensitivity warning.
func newTokenCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage API tokens"}
	cmd.AddCommand(
		listSubCmd(e, "tokens"),
		tokenMintCmd(e),
		tokenRevokeCmd(e),
	)
	return cmd
}

// tokenMintCmd: POST /api/v1/tokens → {"refresh_token","scope","status"}. The refresh token is shown
// ONCE; warn it's sensitive. Accepts k=v fields (e.g. scope=…) like add.
func tokenMintCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "mint [k=v…]",
		Short: "Mint a new token (refresh token shown once)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			body, err := parseItemArgs(args)
			if err != nil {
				return err
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			item, raw, err := c.Create(e.ctx(), "tokens", body, e.scopeProject(), "")
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return render.JSON(e.out, raw)
			}
			_, _ = fmt.Fprintln(e.err, "warning: the refresh token below is shown once — store it now")
			render.Item(e.out, item)
			return nil
		},
	}
}

// tokenRevokeCmd: DELETE /api/v1/tokens/{id} — revoke a token by id.
func tokenRevokeCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			raw, err := c.Delete(e.ctx(), "tokens", args[0], e.scopeProject(), "")
			if err != nil {
				return err
			}
			if e.jsonOut() {
				if len(raw) > 0 {
					return e.renderJSON("tokens-revoke-"+args[0], raw)
				}
				return e.renderJSON("tokens-revoke-"+args[0], []byte(`{"revoked":"`+args[0]+`"}`))
			}
			_, _ = fmt.Fprintf(e.out, "revoked token %s\n", args[0])
			return nil
		},
	}
}

// parseJSONOrItemArgs accepts EITHER a single JSON-object argument (a leading "{") parsed as the whole
// body, OR k=v pairs (parseItemArgs). It lets a nested-path PATCH take a verbatim JSON blob for
// structured controls while keeping the k=v ergonomics for the common flat case.
func parseJSONOrItemArgs(args []string) (map[string]any, error) {
	if len(args) == 1 && strings.HasPrefix(strings.TrimSpace(args[0]), "{") {
		var body map[string]any
		if err := json.Unmarshal([]byte(args[0]), &body); err != nil {
			return nil, fmt.Errorf("parse JSON body: %w", err)
		}
		return body, nil
	}
	return parseItemArgs(args)
}

// parseItemArgs turns k=v field args into a flat create/patch body. Keys pass through verbatim (the
// server owns each resource's field whitelist + validation); values are strings — collection item
// fields are flat scalars, and the server validates types, so the CLI doesn't guess.
func parseItemArgs(args []string) (map[string]any, error) {
	body := make(map[string]any, len(args))
	for _, arg := range args {
		key, val, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid argument %q: expected key=value", arg)
		}
		body[key] = val
	}
	return body, nil
}
