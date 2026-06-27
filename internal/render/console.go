package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

func Capabilities(w io.Writer, r *client.CapabilitiesResponse) {
	_, _ = fmt.Fprintf(w, "Project: %s\n", r.Project)
	if r.Tenant != "" {
		_, _ = fmt.Fprintf(w, "Tenant:  %s\n", r.Tenant)
	}
	_, _ = fmt.Fprintf(w, "Egress:  %s\n", r.EgressMode)
	if len(r.Databases) > 0 {
		_, _ = fmt.Fprintln(w, "\nDatabases:")
		DBList(w, &client.DBListResponse{Databases: r.Databases})
	}
	if len(r.Scripts) > 0 {
		_, _ = fmt.Fprintln(w, "\nScripts:")
		BashList(w, &client.BashListResponse{Scripts: r.Scripts})
	}
	if len(r.Actions) > 0 {
		_, _ = fmt.Fprintln(w, "\nActions:")
		ActionList(w, &client.ActionListResponse{Actions: r.Actions})
	}
}

func DBList(w io.Writer, r *client.DBListResponse) {
	if len(r.Databases) == 0 {
		_, _ = fmt.Fprintln(w, "(no databases)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tENV\tSCOPED\tPII\tDESCRIPTION")
	for _, d := range r.Databases {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", d.Name, d.Env, yesNo(d.Scoped), yesNo(d.PIIMasked), d.Description)
	}
	_ = tw.Flush()
}

func DBSchema(w io.Writer, r *client.DBSchemaResponse) {
	if len(r.Tables) == 0 {
		_, _ = fmt.Fprintln(w, "(no tables)")
		return
	}
	for _, t := range r.Tables {
		_, _ = fmt.Fprintf(w, "%s.%s\n", t.Schema, t.Name)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, c := range t.Columns {
			null := "not null"
			if c.Nullable {
				null = "nullable"
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", c.Name, c.Type, null)
		}
		_ = tw.Flush()
	}
}

func DBQuery(w io.Writer, r *client.DBQueryResponse) {
	if len(r.Rows) == 0 {
		_, _ = fmt.Fprintf(w, "0 rows (%d ms)\n", r.DurationMs)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, strings.Join(r.Columns, "\t"))
	for _, row := range r.Rows {
		vals := make([]string, len(r.Columns))
		for i, col := range r.Columns {
			vals[i] = scalar(row[col])
		}
		_, _ = fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	_ = tw.Flush()
	if r.Truncated {
		_, _ = fmt.Fprintf(w, "\n(truncated at %d rows)\n", r.RowCount)
	}
	_, _ = fmt.Fprintf(w, "\n%d rows (%d ms) run=%s\n", r.RowCount, r.DurationMs, r.RunID)
}

func BashList(w io.Writer, r *client.BashListResponse) {
	if len(r.Scripts) == 0 {
		_, _ = fmt.Fprintln(w, "(no cataloged scripts)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tPATH\tARGS\tPURPOSE")
	for _, s := range r.Scripts {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Path, s.Args, s.Purpose)
	}
	_ = tw.Flush()
}

func BashRun(w io.Writer, r *client.BashRunResponse) {
	if r.Stdout != "" {
		_, _ = fmt.Fprint(w, r.Stdout)
		if !strings.HasSuffix(r.Stdout, "\n") {
			_, _ = fmt.Fprintln(w)
		}
	}
	if r.Stderr != "" {
		if r.Stdout != "" {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintln(w, "stderr:")
		_, _ = fmt.Fprint(w, r.Stderr)
		if !strings.HasSuffix(r.Stderr, "\n") {
			_, _ = fmt.Fprintln(w)
		}
	}
	var flags []string
	if r.TimedOut {
		flags = append(flags, "timed_out")
	}
	if r.EgressBlocked {
		flags = append(flags, "egress_blocked")
	}
	if r.StdoutTruncated {
		flags = append(flags, "stdout truncated")
	}
	if r.StderrTruncated {
		flags = append(flags, "stderr truncated")
	}
	suffix := ""
	if len(flags) > 0 {
		suffix = " (" + strings.Join(flags, ", ") + ")"
	}
	_, _ = fmt.Fprintf(w, "\nexit=%d (%dms) run=%s seq=%d%s\n", r.ExitCode, r.DurationMs, r.RunID, r.Seq, suffix)
}

func ActionList(w io.Writer, r *client.ActionListResponse) {
	if len(r.Actions) == 0 {
		_, _ = fmt.Fprintln(w, "(no actions)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPREFLIGHT\tRISK\tDESCRIPTION")
	for _, a := range r.Actions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.ID, yesNo(a.Preflight), a.Risk, a.Description)
	}
	_ = tw.Flush()
}

func ActionShow(w io.Writer, r *client.ActionShowResponse) {
	_, _ = fmt.Fprintf(w, "Action:    %s\nDigest:    %s\nPreflight: %s\n", r.ID, r.Digest, yesNo(r.Preflight))
	if len(r.Manifest) > 0 {
		_, _ = fmt.Fprintln(w, "\nManifest:")
		_ = JSON(w, r.Manifest)
	}
}

func ActionExec(w io.Writer, r *client.ActionExecResponse) {
	_, _ = fmt.Fprintf(w, "Action run: %s\nStatus:     %s\nDry run:    %s\nDuration:   %d ms\n", r.ID, r.Status, yesNo(r.DryRun), r.DurationMs)
	if len(r.Error) > 0 && string(r.Error) != "null" {
		_, _ = fmt.Fprintln(w, "\nError:")
		_ = JSON(w, r.Error)
	}
	if len(r.Result) > 0 && string(r.Result) != "null" {
		_, _ = fmt.Fprintln(w, "\nResult:")
		_ = JSON(w, r.Result)
	}
}

func scalar(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "\n", `\n`)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}
