// This file renders the connection-backed watched-mailbox views under `rc project mailbox`: the live
// inbox watch with its subscription/sync-cursor lifecycle.
package render

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// WatchedMailboxes renders the watched-mailbox set as a table: id, provider, email, status, mode, tenant,
// subscription expiry, and any error message. Empty → "(none)". A pure function of the wire items so a
// golden pins it.
func WatchedMailboxes(w io.Writer, l *client.WatchedMailboxList) {
	if l == nil || len(l.Mailboxes) == 0 {
		_, _ = fmt.Fprintln(w, "(none)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tPROVIDER\tEMAIL\tSTATUS\tMODE\tPROCESSING\tTENANT\tSUB-EXPIRES\tLAST-SYNC\tERROR")
	for _, m := range l.Mailboxes {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID, m.Provider, m.EmailAddress, m.Status, orDash(m.Mode, "-"), processingLabel(m.ProcessingEnabled),
			orDash(m.Tenant, "-"), orDash(m.SubscriptionExpiresAt, "-"), lastSyncLabel(m), orDash(m.ErrorMessage, "-"))
	}
	_ = tw.Flush()
}

// lastSyncLabel renders ingest liveness. "never" is the honest answer for a mailbox that has not
// completed a sync yet — a poll mailbox has no subscription expiry, so without this column a silently
// dead IMAP mailbox is indistinguishable from a healthy one in the table.
func lastSyncLabel(m client.WatchedMailbox) string {
	if m.LastSuccessfulSyncAt == "" {
		return "never"
	}
	if m.ConsecutiveSyncFailures > 0 {
		return fmt.Sprintf("%s (%d failing)", m.LastSuccessfulSyncAt, m.ConsecutiveSyncFailures)
	}
	return m.LastSuccessfulSyncAt
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
	if m.Mode != "" {
		_, _ = fmt.Fprintf(tw, "mode:\t%s\n", m.Mode)
	}
	_, _ = fmt.Fprintf(tw, "processing:\t%s\n", processingLabel(m.ProcessingEnabled))
	if m.Tenant != "" {
		_, _ = fmt.Fprintf(tw, "tenant:\t%s\n", m.Tenant)
	}
	if m.SubscriptionExpiresAt != "" {
		_, _ = fmt.Fprintf(tw, "sub-expires:\t%s\n", m.SubscriptionExpiresAt)
	}
	_, _ = fmt.Fprintf(tw, "last-sync:\t%s\n", lastSyncLabel(*m))
	if m.ErrorMessage != "" {
		_, _ = fmt.Fprintf(tw, "error:\t%s\n", m.ErrorMessage)
	}
	_ = tw.Flush()
}

// imapStepLabels maps the server's stable step ids to the human names shown in the checklist. An
// unknown id (newer server, older CLI) falls through to the raw id rather than being dropped.
var imapStepLabels = map[string]string{
	"config":             "Settings are valid",
	"imap.connect":       "Reach the IMAP server",
	"imap.login":         "Sign in over IMAP",
	"imap.select_inbox":  "Open the INBOX",
	"imap.drafts_append": "Place a draft for review",
	"smtp.connect":       "Reach the SMTP server",
	"smtp.auth":          "Sign in over SMTP",
}

// imapStepMark is the leading glyph per status. ASCII-safe so it survives a piped/CI terminal.
func imapStepMark(status string) string {
	switch status {
	case "ok":
		return "[ok]  "
	case "failed":
		return "[FAIL]"
	case "warning":
		return "[warn]"
	default:
		return "[--]  "
	}
}

func imapStepLabel(name string) string {
	if l := imapStepLabels[name]; l != "" {
		return l
	}
	return name
}

// IMAPProbeSteps renders the connection checklist — one line per stage, with the actionable hint
// indented under any stage that isn't plainly OK. This is the whole point of the probe: the reader
// must be able to tell WHICH of host / port / TLS mode / credentials to change.
func IMAPProbeSteps(w io.Writer, steps []client.IMAPProbeStep) {
	for _, s := range steps {
		_, _ = fmt.Fprintf(w, "%s %s\n", imapStepMark(s.Status), imapStepLabel(s.Name))
		if s.Detail != "" && s.Status != "ok" {
			_, _ = fmt.Fprintf(w, "       %s\n", s.Detail)
		}
	}
}

// IMAPProbe renders a full probe result: the checklist plus a one-line verdict.
func IMAPProbe(w io.Writer, p *client.IMAPProbe) {
	if p == nil {
		return
	}
	IMAPProbeSteps(w, p.Steps)
	switch {
	case !p.OK:
		_, _ = fmt.Fprintln(w, "\nnot connected — fix the failing step above and re-run")
	case imapProbeHasWarning(p.Steps):
		_, _ = fmt.Fprintln(w, "\nconnected, with a limitation (see the warning above)")
	default:
		_, _ = fmt.Fprintln(w, "\nconnected — all checks passed")
	}
}

func imapProbeHasWarning(steps []client.IMAPProbeStep) bool {
	for _, s := range steps {
		if s.Status == "warning" {
			return true
		}
	}
	return false
}
