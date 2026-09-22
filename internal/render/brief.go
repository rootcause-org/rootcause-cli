// This file renders `rc run trace <id> --brief`: ONE turn, read the way a human reads it — what was
// asked, what we answered, how it ended. The forensic half of the bundle (system prompt, prompt
// sections, grounding snapshots, tenant-settings blobs, the tool-call timeline) is deliberately absent;
// `rc run trace <id> --raw-output` and `rc run debug <id>` stay the place for that.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
)

// Brief renders the reading view of one run.
func Brief(w io.Writer, f *client.FullResponse) {
	r := &f.Run
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run:\t%s\n", r.RunID)
	_, _ = fmt.Fprintf(tw, "Status:\t%s\n", r.Status)
	if oc := metaString(r.Metadata, "outcome"); oc != "" {
		_, _ = fmt.Fprintf(tw, "Outcome:\t%s\n", oc)
	}
	_, _ = fmt.Fprintf(tw, "Kind:\t%s\n", r.Kind)
	if r.Tenant != "" {
		_, _ = fmt.Fprintf(tw, "Tenant:\t%s\n", r.Tenant)
	}
	if p := digest.PrincipalID(r.Guards); p != "" {
		_, _ = fmt.Fprintf(tw, "Principal:\t%s\n", p)
	}
	_, _ = fmt.Fprintf(tw, "Created:\t%s\n", r.CreatedAt)
	if r.FinishedAt != "" {
		_, _ = fmt.Fprintf(tw, "Finished:\t%s\n", r.FinishedAt)
	}
	if n := jsonArrayLen(r.PriorMessages); n > 0 {
		_, _ = fmt.Fprintf(tw, "Prior messages:\t%d\n", n)
	}
	if outbound := outboundEmailLabel(r.OutboundEmail); outbound != "" {
		_, _ = fmt.Fprintf(tw, "Outbound:\t%s\n", outbound)
	}
	if egress := briefEgressLabel(r.Egress); egress != "" {
		_, _ = fmt.Fprintf(tw, "Egress:\t%s\n", egress)
	}
	drift, _ := digest.TenantSettingsDrift(r.TenantSettings, r.TenantSettingsCurrent)
	if len(drift) > 0 {
		_, _ = fmt.Fprintf(tw, "Tenant settings drift:\t%d changed\n", len(drift))
	}
	if u := metaString(r.Metadata, "run_url"); u != "" {
		_, _ = fmt.Fprintf(tw, "View run:\t%s\n", u)
	}
	_ = tw.Flush()

	if f.Redacted() {
		_, _ = fmt.Fprintf(w, "\n%s\n", RedactedTraceNotice)
	}
	if r.Question != "" {
		_, _ = fmt.Fprintf(w, "\nQuestion:\n%s\n", strings.TrimSpace(r.Question))
	}
	for _, body := range []struct{ label, text string }{
		{"Answer", r.Draft},
		{"Answer", r.AnswerMarkdown},
		{"Decline", r.Decline},
	} {
		if s := strings.TrimSpace(body.text); s != "" {
			_, _ = fmt.Fprintf(w, "\n%s:\n%s\n", body.label, s)
			break
		}
	}
	for _, n := range r.Notes {
		if body := strings.TrimSpace(n.Body); body != "" {
			_, _ = fmt.Fprintf(w, "\nNote (%s):\n%s\n", n.Key, body)
		}
	}
	if r.Error != "" {
		_, _ = fmt.Fprintf(w, "\nError:\n%s\n", r.Error)
	}
	_, _ = fmt.Fprintln(w, "\nPrompt, grounding and the tool-call timeline are omitted; use `rc run trace <id> --raw-output` or `rc run debug <id>`.")
}

// briefEgressLabel compresses the whole egress list into one cell: how many hosts, how many blocked —
// the security-relevant fact, without the per-host table the forensic view already prints.
func briefEgressLabel(hosts []client.EgressItem) string {
	if len(hosts) == 0 {
		return ""
	}
	blocked := 0
	for _, h := range hosts {
		if h.Blocked {
			blocked++
		}
	}
	if blocked == 0 {
		return countLabel(len(hosts), "host")
	}
	return fmt.Sprintf("%s, %d blocked", countLabel(len(hosts), "host"), blocked)
}

// jsonArrayLen counts the elements of a raw JSON array, 0 for anything else (null, an object, a body
// we cannot parse) — a count is a hint here, never a fact worth failing on.
func jsonArrayLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return 0
	}
	return len(items)
}
