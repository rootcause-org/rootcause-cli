package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
)

func Capabilities(w io.Writer, r *client.CapabilitiesResponse) {
	_, _ = fmt.Fprintf(w, "Project: %s\n", r.Project)
	if r.Tenant != "" {
		_, _ = fmt.Fprintf(w, "Tenant:  %s\n", r.Tenant)
	}
	_, _ = fmt.Fprintf(w, "Egress:  %s\n", r.EgressMode)
	if r.Brain.Ref != "" || r.Brain.State != "" {
		_, _ = fmt.Fprintln(w, "\nBrain:")
		BrainStatus(w, &client.BrainStatusResponse{Project: r.Project, Status: r.Brain})
	}
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
	_, _ = fmt.Fprintln(tw, "NAME\tENV\tSCOPED\tPII\tWRITABLE\tDESCRIPTION")
	for _, d := range r.Databases {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.Name, d.Env, yesNo(d.Scoped), yesNo(d.PIIMasked), yesNo(d.Writable), d.Description)
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
	if r.DryRun {
		_, _ = fmt.Fprintln(w, "DRY RUN — rolled back, nothing committed")
	}
	// A write (rows_affected present) leads with its commit count; the RETURNING rows, if any, still
	// render below through the same row table.
	if r.RowsAffected != nil {
		_, _ = fmt.Fprintf(w, "rows affected: %d\n", *r.RowsAffected)
	}
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

// ScopePreview renders a scope-preview report: the resolved scope header, then per-table counts + the
// compiled predicate + sample rows. It is the table twin of the -o json PreviewReport.
func ScopePreview(w io.Writer, r *client.ScopePreviewReport) {
	_, _ = fmt.Fprintf(w, "Project: %s  DB: %s\n", r.Project, r.DSNEnv)
	if r.TenantPredicate {
		_, _ = fmt.Fprintf(w, "Tenant:  %s (scope_value=%s)\n", r.Tenant, r.ScopeValue)
	} else {
		_, _ = fmt.Fprintln(w, "Tenant:  (flat / cross-tenant)")
	}
	if r.Principal != nil {
		_, _ = fmt.Fprintf(w, "Principal: %s=%s\n", r.Principal.Kind, r.Principal.ExternalID)
	}
	if len(r.Claims) > 0 {
		parts := make([]string, 0, len(r.Claims))
		for _, k := range sortedMapKeys(r.Claims) {
			parts = append(parts, k+"="+r.Claims[k])
		}
		_, _ = fmt.Fprintf(w, "Claims:  %s\n", strings.Join(parts, " "))
	}
	if len(r.Tables) == 0 {
		_, _ = fmt.Fprintln(w, "\n(no reachable tables)")
		return
	}
	for _, tb := range r.Tables {
		_, _ = fmt.Fprintf(w, "\n%s — %d row(s)\n", tb.Name, tb.Count)
		if tb.Predicate != "" {
			_, _ = fmt.Fprintf(w, "  where: %s\n", tb.Predicate)
		}
		if len(tb.Rows) == 0 {
			continue
		}
		cols := sortedMapKeys(tb.Rows[0])
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  "+strings.Join(cols, "\t"))
		for _, row := range tb.Rows {
			vals := make([]string, len(cols))
			for i, c := range cols {
				vals[i] = scalar(row[c])
			}
			_, _ = fmt.Fprintln(tw, "  "+strings.Join(vals, "\t"))
		}
		_ = tw.Flush()
	}
}

func BashList(w io.Writer, r *client.BashListResponse) {
	if r.Brain.Ref != "" || r.Brain.State != "" {
		BrainStatus(w, &client.BrainStatusResponse{Project: r.Project, Status: r.Brain})
		_, _ = fmt.Fprintln(w)
	}
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

func BashRun(w io.Writer, r *client.BashRunResponse, artifacts map[string]outputspill.Artifact) {
	if r.Stdout != "" {
		renderBashStream(w, r.Stdout, artifacts["stdout"])
	}
	if r.Stderr != "" {
		if r.Stdout != "" {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintln(w, "stderr:")
		renderBashStream(w, r.Stderr, artifacts["stderr"])
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
	if r.BrainResolved != "" {
		flags = append(flags, "brain "+r.BrainResolved)
	}
	suffix := ""
	if len(flags) > 0 {
		suffix = " (" + strings.Join(flags, ", ") + ")"
	}
	_, _ = fmt.Fprintf(w, "\nexit=%d (%dms) run=%s seq=%d%s\n", r.ExitCode, r.DurationMs, r.RunID, r.Seq, suffix)
}

func renderBashStream(w io.Writer, value string, art outputspill.Artifact) {
	if art.Path == "" {
		_, _ = fmt.Fprint(w, value)
		if !strings.HasSuffix(value, "\n") {
			_, _ = fmt.Fprintln(w)
		}
		return
	}
	_, _ = fmt.Fprintf(w, "[output too large: %d bytes, %d lines - full output saved to %s]\n", art.Bytes, art.Lines, art.Path)
	if art.Preview != nil {
		if art.Preview.Head != "" {
			_, _ = fmt.Fprint(w, art.Preview.Head)
			if !strings.HasSuffix(art.Preview.Head, "\n") {
				_, _ = fmt.Fprintln(w)
			}
		}
		if art.Preview.Tail != "" {
			_, _ = fmt.Fprintln(w, "...[middle omitted]...")
			_, _ = fmt.Fprint(w, art.Preview.Tail)
			if !strings.HasSuffix(art.Preview.Tail, "\n") {
				_, _ = fmt.Fprintln(w)
			}
		}
	}
	if len(art.Hints) > 0 {
		_, _ = fmt.Fprintln(w, "\nHints:")
		for _, h := range art.Hints {
			_, _ = fmt.Fprintf(w, "  %s\n", h)
		}
	}
}

func BrainStatus(w io.Writer, r *client.BrainStatusResponse) {
	st := r.Status
	if !st.Available {
		msg := st.Message
		if msg == "" {
			msg = "not available"
		}
		_, _ = fmt.Fprintf(w, "Brain: %s (%s)\n", r.Project, msg)
		return
	}
	state := st.State
	if state == "" {
		state = "unknown"
	}
	_, _ = fmt.Fprintf(w, "Project: %s\n", r.Project)
	_, _ = fmt.Fprintf(w, "Ref:     %s\n", st.Ref)
	_, _ = fmt.Fprintf(w, "Local:   %s\n", shortGit(st.LocalSHA))
	if st.RemoteSHA != "" {
		_, _ = fmt.Fprintf(w, "Origin:  %s\n", shortGit(st.RemoteSHA))
	}
	_, _ = fmt.Fprintf(w, "State:   %s", state)
	if st.Ahead > 0 || st.Behind > 0 {
		_, _ = fmt.Fprintf(w, " (ahead %d, behind %d)", st.Ahead, st.Behind)
	}
	if st.Stale {
		_, _ = fmt.Fprint(w, " stale")
	}
	_, _ = fmt.Fprintln(w)
	if st.SyncedAt != "" {
		_, _ = fmt.Fprintf(w, "Synced:  %s\n", st.SyncedAt)
	}
	if st.Message != "" {
		_, _ = fmt.Fprintf(w, "Note:    %s\n", st.Message)
	}
	if len(st.Channels) > 0 {
		_, _ = fmt.Fprintln(w, "\nChannels:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "CHANNEL\tRESOLVED\tORIGIN\tMAIN\tORIGIN?\tMAIN?\tSTATE\tPROVENANCE")
		for _, ch := range st.Channels {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				ch.Channel, dash(shortGit(ch.ResolvedSHA)), dash(shortGit(ch.OriginSHA)), dash(shortGit(ch.MainSHA)),
				yesNo(ch.MatchesOrigin), yesNo(ch.MatchesMain), dash(ch.State), dash(ch.Provenance))
		}
		_ = tw.Flush()
	}
}

func BrainSync(w io.Writer, r *client.BrainSyncResponse) {
	st := r.Sync.After
	_, _ = fmt.Fprintf(w, "Project: %s\n", r.Project)
	_, _ = fmt.Fprintf(w, "Fetched: %s\n", yesNo(r.Sync.Fetched))
	_, _ = fmt.Fprintf(w, "Updated: %s\n", yesNo(r.Sync.FastForwarded))
	if r.Sync.RefreshedWorkspaces > 0 {
		_, _ = fmt.Fprintf(w, "Refresh: %d workspace(s)\n", r.Sync.RefreshedWorkspaces)
	}
	if r.Sync.ManualReconcile {
		_, _ = fmt.Fprintln(w, "Action:  manual reconcile required")
	}
	if r.Sync.Message != "" {
		_, _ = fmt.Fprintf(w, "Note:    %s\n", r.Sync.Message)
	}
	_, _ = fmt.Fprintln(w)
	BrainStatus(w, &client.BrainStatusResponse{Project: r.Project, Status: st})
}

func BrainPromote(w io.Writer, r *client.BrainPromoteResponse) {
	state := "changed"
	if r.Idempotent || !r.Changed {
		state = "idempotent"
	}
	_, _ = fmt.Fprintf(w, "Project: %s\n", r.Project)
	_, _ = fmt.Fprintf(w, "Channel: %s\n", r.Channel)
	_, _ = fmt.Fprintf(w, "Old:     %s\n", dash(shortGit(r.OldSHA)))
	_, _ = fmt.Fprintf(w, "New:     %s\n", dash(shortGit(r.NewSHA)))
	_, _ = fmt.Fprintf(w, "State:   %s\n", state)
}

func ActionList(w io.Writer, r *client.ActionListResponse) {
	if len(r.Actions) == 0 {
		_, _ = fmt.Fprintln(w, "(no actions)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tDISPLAY NAME\tRISK\tPREFLIGHT\tAUTONOMY\tSUCCESS\tLAST RUN")
	for _, a := range r.Actions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			a.ID, dash(a.DisplayName), dash(a.Risk), yesNo(a.Preflight || a.HasPreflight),
			dash(a.Autonomy.Effective), actionSuccessCell(a.Stats), actionLastRunCell(a.Stats))
	}
	_ = tw.Flush()
}

func ActionShow(w io.Writer, r *client.ActionShowResponse) {
	c := r.Catalog
	_, _ = fmt.Fprintf(w, "Action:      %s\n", r.ID)
	if c.DisplayName != "" {
		_, _ = fmt.Fprintf(w, "Name:        %s\n", c.DisplayName)
	}
	if c.Description != "" {
		_, _ = fmt.Fprintf(w, "Description: %s\n", c.Description)
	}
	if c.Risk != "" {
		_, _ = fmt.Fprintf(w, "Risk:        %s\n", c.Risk)
	}
	_, _ = fmt.Fprintf(w, "Preflight:   %s\n", yesNo(r.Preflight || c.HasPreflight))
	_, _ = fmt.Fprintf(w, "Policy:      %s\n", yesNo(c.HasPolicy))
	_, _ = fmt.Fprintf(w, "Digest:      %s\n", r.Digest)

	if c.Autonomy.Manifest != "" || c.Autonomy.Cap != "" || c.Autonomy.Effective != "" {
		_, _ = fmt.Fprintf(w, "\nAutonomy:    manifest %s -> cap %s -> effective %s\n",
			dash(c.Autonomy.Manifest), dash(c.Autonomy.Cap), dash(c.Autonomy.Effective))
		for _, f := range c.Autonomy.Floors {
			_, _ = fmt.Fprintf(w, "  - %s\n", f)
		}
	}

	if len(c.Connections) > 0 {
		_, _ = fmt.Fprintln(w, "\nConnections:")
		ctw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, conn := range c.Connections {
			state := "missing"
			if conn.Satisfied {
				state = "granted"
			}
			_, _ = fmt.Fprintf(ctw, "  %s\t%s\n", conn.Key, state)
		}
		_ = ctw.Flush()
	}

	if len(c.Params) > 0 {
		_, _ = fmt.Fprintln(w, "\nParams:")
		ptw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(ptw, "  NAME\tTYPE\tREQUIRED\tDESCRIPTION")
		for _, p := range c.Params {
			_, _ = fmt.Fprintf(ptw, "  %s\t%s\t%s\t%s\n", p.Name, dash(p.Type), yesNo(p.Required), p.Description)
		}
		_ = ptw.Flush()
	}

	s := c.Stats
	if s.Total > 0 || s.Proposed > 0 || s.Executing > 0 {
		_, _ = fmt.Fprintln(w, "\nStats (window):")
		_, _ = fmt.Fprintf(w, "  runs:       %d (succeeded %d, failed %d, proposed %d, executing %d, canceled %d)\n",
			s.Total, s.Succeeded, s.Failed, s.Proposed, s.Executing, s.Canceled)
		_, _ = fmt.Fprintf(w, "  success:    %s\n", actionSuccessCell(s))
		if s.P50DurationMs > 0 {
			_, _ = fmt.Fprintf(w, "  p50:        %.0f ms\n", s.P50DurationMs)
		}
		if s.LastProposedAt != nil {
			_, _ = fmt.Fprintf(w, "  proposed:   %s\n", s.LastProposedAt.Format("2006-01-02 15:04"))
		}
		if s.LastExecutedAt != nil {
			_, _ = fmt.Fprintf(w, "  executed:   %s\n", s.LastExecutedAt.Format("2006-01-02 15:04"))
		}
	}

	if len(r.Manifest) > 0 {
		_, _ = fmt.Fprintln(w, "\nManifest:")
		_ = JSON(w, r.Manifest)
	}
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// actionSuccessCell renders "8/10 80%" over settled runs, or "—" when no run has settled (SuccessRate nil).
func actionSuccessCell(s client.ActionStats) string {
	if s.SuccessRate == nil {
		return "—"
	}
	settled := s.Succeeded + s.Failed
	return fmt.Sprintf("%d/%d %.0f%%", s.Succeeded, settled, *s.SuccessRate*100)
}

func actionLastRunCell(s client.ActionStats) string {
	t := s.LastExecutedAt
	if t == nil {
		t = s.LastProposedAt
	}
	if t == nil {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
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

// sortedMapKeys returns a map's keys in stable order — deterministic column/claim ordering for tables.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

func shortGit(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
