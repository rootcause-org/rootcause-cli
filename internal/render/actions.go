package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// Actions renders either the human index+details view or a token-lean agent view. Both expose the exact
// grounded params and full tokenized run URL without truncation.
func Actions(w io.Writer, items []client.ActionFeedItem, format string) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(w, "(no actions)")
		return
	}
	if format == "agent" {
		actionsAgent(w, items)
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ACTION_RUN\tRUN\tTENANT\tACTION\tSTATUS\tPROPOSED\tEXECUTED\tDURATION")
	for _, item := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID,
			nullableString(item.RunID),
			nullableString(item.TenantID),
			item.ActionID,
			item.Status,
			item.ProposedAt,
			nullableString(item.ExecutedAt),
			nullableDuration(item.DurationMs),
		)
	}
	_ = tw.Flush()

	_, _ = fmt.Fprintln(w, "\nDetails")
	for _, item := range items {
		params := compactBody(item.Params)
		if params == "" {
			params = "-"
		}
		_, _ = fmt.Fprintf(w, "%s\n  Params: %s\n  Run URL: %s\n",
			item.ID, params, nullableString(item.RunURL))
	}
}

func actionsAgent(w io.Writer, items []client.ActionFeedItem) {
	for _, item := range items {
		params := compactBody(item.Params)
		if params == "" {
			params = "null"
		}
		_, _ = fmt.Fprintf(w,
			"ACTION id=%s run_id=%s tenant_id=%s action_id=%s status=%s proposed_at=%s executed_at=%s duration_ms=%s params=%s run_url=%s\n",
			item.ID,
			agentValue(item.RunID),
			agentValue(item.TenantID),
			strconv.Quote(item.ActionID),
			strconv.Quote(item.Status),
			strconv.Quote(item.ProposedAt),
			agentValue(item.ExecutedAt),
			agentDuration(item.DurationMs),
			params,
			agentValue(item.RunURL),
		)
	}
}

func agentValue(value *string) string {
	if value == nil {
		return "null"
	}
	b, _ := json.Marshal(*value)
	return string(b)
}

func agentDuration(value *int64) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatInt(*value, 10)
}

func nullableString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}

func nullableDuration(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%dms", *value)
}
