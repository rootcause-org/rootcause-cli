package render

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// SettingProposals renders the review queue: one row per proposal with a one-line change summary. The full
// payload, evidence and rationale stay on `show` / -o json.
func SettingProposals(w io.Writer, items []client.SettingProposal) {
	if len(items) == 0 {
		_, _ = fmt.Fprintln(w, "(no settings proposals)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tSCOPE\tKIND\tCHANGE\tSOURCE\tCREATED")
	for _, p := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			clipID(p.ID, 8), p.Status, proposalScope(p), p.Kind, truncate(ProposalChange(p), 60),
			orDash(p.Source, "-"), orDash(clipID(p.CreatedAt, 10), "-"))
	}
	_ = tw.Flush()
}

func proposalScope(p client.SettingProposal) string {
	switch {
	case p.Tenant != "":
		return "tenant " + p.Tenant
	case p.MailboxID != "":
		return "mailbox " + clipID(p.MailboxID, 8)
	default:
		return orDash(p.Level, "-")
	}
}

// ProposalChange is the one-line "what would change" for a proposal, derived from its per-kind payload.
func ProposalChange(p client.SettingProposal) string {
	var v struct {
		Group, Key, Bag, Operation string
		Value                      json.RawMessage
		Guidance                   string
		Effect                     string
		MatchKind                  string `json:"match_kind"`
		Pattern                    string
	}
	_ = json.Unmarshal(p.Payload, &v)
	switch p.Kind {
	case "hierarchy_set", "bag_set":
		prefix := v.Group
		if p.Kind == "bag_set" {
			prefix = v.Bag
		}
		if v.Operation == "clear" {
			return prefix + "." + v.Key + " → inherit"
		}
		return prefix + "." + v.Key + " = " + settingValue(v.Value)
	case "triage_policy_set":
		return "guidance = " + v.Guidance
	case "triage_rule_add":
		return v.Effect + " " + v.MatchKind + "=" + v.Pattern
	}
	return "-"
}
