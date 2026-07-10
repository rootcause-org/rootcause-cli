// This file renders the connection-backed watched-mailbox views under `rc project mailbox`: the live
// inbox watch with its subscription/sync-cursor lifecycle.
package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// WatchedMailboxes renders the watched-mailbox set as a table: id, provider, email, status, tenant,
// subscription expiry, and any error message. Empty → "(none)". A pure function of the wire items so a
// golden pins it.
func WatchedMailboxes(w io.Writer, l *client.WatchedMailboxList) {
	if l == nil || len(l.Mailboxes) == 0 {
		_, _ = fmt.Fprintln(w, "(none)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPROVIDER\tEMAIL\tSTATUS\tPROCESSING\tTENANT\tSUB-EXPIRES\tERROR")
	for _, m := range l.Mailboxes {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.Provider, m.EmailAddress, m.Status, processingLabel(m.ProcessingEnabled),
			strOrBlank(m.Tenant), strOrBlank(m.SubscriptionExpiresAt), strOrBlank(m.ErrorMessage))
	}
	_ = tw.Flush()
}

// processingLabel renders the silent-onboarding gate in plain words for the table/detail views.
func processingLabel(on bool) string {
	if on {
		return "on"
	}
	return "silent"
}

// WatchedMailbox renders one updated mailbox (the pause/resume echo) as a key: value block. When the
// status is needs_attention it surfaces the error_message prominently — a resume that hit a Subscribe
// failure is still a 200, and the message is the actionable signal.
func WatchedMailbox(w io.Writer, m *client.WatchedMailbox) {
	if m == nil {
		_, _ = fmt.Fprintln(w, "(no mailbox returned)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "id:\t%s\n", m.ID)
	_, _ = fmt.Fprintf(tw, "provider:\t%s\n", m.Provider)
	_, _ = fmt.Fprintf(tw, "email:\t%s\n", m.EmailAddress)
	_, _ = fmt.Fprintf(tw, "status:\t%s\n", m.Status)
	_, _ = fmt.Fprintf(tw, "processing:\t%s\n", processingLabel(m.ProcessingEnabled))
	if m.Tenant != "" {
		_, _ = fmt.Fprintf(tw, "tenant:\t%s\n", m.Tenant)
	}
	if m.SubscriptionExpiresAt != "" {
		_, _ = fmt.Fprintf(tw, "sub-expires:\t%s\n", m.SubscriptionExpiresAt)
	}
	if m.ErrorMessage != "" {
		_, _ = fmt.Fprintf(tw, "error:\t%s\n", m.ErrorMessage)
	}
	_ = tw.Flush()
}
