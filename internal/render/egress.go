package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// RunEgress renders both outbound planes: the gateway is the always-on connection backstop, while
// the cooperative HTTP rows carry method, endpoint, payload, retry, status, and correlation detail.
func RunEgress(w io.Writer, resp *client.RunEgressResponse) {
	if resp == nil {
		_, _ = fmt.Fprintln(w, "(no egress returned)")
		return
	}
	_, _ = fmt.Fprintf(w, "Run: %s\n\n", resp.RunID)
	_, _ = fmt.Fprintln(w, "HTTP attempts")
	if len(resp.HTTP) == 0 {
		_, _ = fmt.Fprintln(w, "  (none)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  AT\tSOURCE\tMETHOD\tHOST\tENDPOINT\tSTATUS\tTRY\tREASON\tBYTES\tREQUEST_ID")
		for _, row := range resp.HTTP {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t%s\n",
				row.At, row.Source, row.Method, row.Host, endpointOf(row), httpStatus(row.StatusCode), row.Attempt,
				blank(row.Reason), row.RequestBytes, blank(row.RequestID))
		}
		_ = tw.Flush()
		for _, row := range resp.HTTP {
			if len(row.RequestBody) == 0 && row.PayloadSHA256 == "" {
				continue
			}
			_, _ = fmt.Fprintf(w, "  %s %s try %d", row.Method, endpointOf(row), row.Attempt)
			if row.PayloadSHA256 != "" {
				_, _ = fmt.Fprintf(w, " sha256=%s", row.PayloadSHA256)
			}
			if body := compactBody(row.RequestBody); body != "" {
				_, _ = fmt.Fprintf(w, " body=%s", body)
			}
			_, _ = fmt.Fprintln(w)
		}
	}

	_, _ = fmt.Fprintln(w, "\nGateway connections")
	if len(resp.Egress) == 0 {
		_, _ = fmt.Fprintln(w, "  (none)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  AT\tHOST\tPORT\tSCHEME\tDECISION\tBYTES_OUT\tURL")
	for _, row := range resp.Egress {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%d\t%s\t%s\t%d\t%s\n",
			row.At, row.Host, row.Port, row.Scheme, row.Decision, row.BytesOut, blank(row.URL))
	}
	_ = tw.Flush()
}

func ActionHistory(w io.Writer, rows []client.ActionHistoryRow) {
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "(no actions)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tACTION\tSTATUS\tDIGEST\tPARAMS_HASH\tDURATION\tCREATED\tCOMPLETED")
	for _, row := range rows {
		duration := "-"
		if row.DurationMs != nil {
			duration = fmt.Sprintf("%dms", *row.DurationMs)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.ID, row.ActionID, row.Status, row.Digest, row.ParamsHash, duration, row.CreatedAt, blank(row.CompletedAt))
	}
	_ = tw.Flush()
}

type endpointRollup struct {
	source   string
	method   string
	host     string
	endpoint string
	count    int
	blocked  int
	errors   int
	retries  int
	runs     map[string]bool
}

func rollupHTTP(rows []client.HTTPAuditRow) []*endpointRollup {
	groups := map[string]*endpointRollup{}
	for _, row := range rows {
		endpoint := endpointOf(row)
		key := strings.Join([]string{row.Source, row.Method, row.Host, endpoint}, "\x00")
		group := groups[key]
		if group == nil {
			group = &endpointRollup{source: row.Source, method: row.Method, host: row.Host, endpoint: endpoint, runs: map[string]bool{}}
			groups[key] = group
		}
		group.count++
		group.runs[row.RunID] = true
		if row.Decision == "block" {
			group.blocked++
		}
		if row.StatusCode == 0 || row.StatusCode >= 400 {
			group.errors++
		}
		if row.Attempt > 1 {
			group.retries++
		}
	}
	out := make([]*endpointRollup, 0, len(groups))
	for _, group := range groups {
		out = append(out, group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return strings.Join([]string{out[i].host, out[i].endpoint, out[i].method}, "\x00") <
			strings.Join([]string{out[j].host, out[j].endpoint, out[j].method}, "\x00")
	})
	return out
}

// ProjectEgress leads with the endpoint map, then exposes connection-only traffic that bypassed the
// audited primitive. Attribution is deliberately coarse at run+host: TLS keeps the gateway below paths.
func ProjectEgress(w io.Writer, gateway []client.EgressRow, httpRows []client.HTTPAuditRow, days int) {
	_, _ = fmt.Fprintf(w, "Outbound endpoints — last %d days\n", days)
	rollups := rollupHTTP(httpRows)
	if len(rollups) == 0 {
		_, _ = fmt.Fprintln(w, "  (no audited HTTP attempts)")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "SOURCE\tMETHOD\tHOST\tENDPOINT\tCOUNT\tRUNS\tBLOCKED\tERRORS")
		for _, group := range rollups {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\n",
				group.source, group.method, group.host, group.endpoint, group.count, len(group.runs), group.blocked, group.errors)
		}
		_ = tw.Flush()
	}

	attributed := map[string]int{}
	for _, row := range httpRows {
		// Broker traffic originates host-side and does not prove that a workspace gateway connection was
		// emitted by the audited runtime primitive. Only cooperative runtime/action attempts consume a
		// matching gateway connection from the unattributed backstop.
		if row.Source != "broker" {
			attributed[row.RunID+"\x00"+row.Host]++
		}
	}
	unattributed := map[string]int{}
	blocked := map[string]int{}
	for _, row := range gateway {
		if row.Decision == "block" {
			blocked[row.Host]++
			continue
		}
		key := row.RunID + "\x00" + row.Host
		if attributed[key] > 0 {
			attributed[key]--
		} else {
			unattributed[row.Host]++
		}
	}
	renderHostCounts(w, "Blocked gateway connections", blocked)
	renderHostCounts(w, "Unattributed gateway connections", unattributed)
}

func renderHostCounts(w io.Writer, title string, counts map[string]int) {
	_, _ = fmt.Fprintf(w, "\n%s\n", title)
	if len(counts) == 0 {
		_, _ = fmt.Fprintln(w, "  (none)")
		return
	}
	hosts := make([]string, 0, len(counts))
	for host := range counts {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		if counts[hosts[i]] != counts[hosts[j]] {
			return counts[hosts[i]] > counts[hosts[j]]
		}
		return hosts[i] < hosts[j]
	})
	for _, host := range hosts {
		_, _ = fmt.Fprintf(w, "  %s  %d\n", host, counts[host])
	}
}

func endpointOf(row client.HTTPAuditRow) string {
	if row.Endpoint != "" {
		return row.Endpoint
	}
	return blank(row.Path)
}

func httpStatus(status int32) string {
	if status == 0 {
		return "transport_error"
	}
	return fmt.Sprint(status)
}

func blank(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func compactBody(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return string(trimmed)
	}
	return buf.String()
}
