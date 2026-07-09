// This file is the FAT side of `rc health`: it rolls up the THIN /api/v1/health raw rows into the
// healthy/unhealthy sections health.py renders, and reports the overall verdict so the command can exit
// non-zero (CI/cron usable). The server ships raw mirror_health + watched-mailbox + dead-lettered rows
// with NO verdict; the staleness rule (a mirror that hasn't synced in >STALE_HOURS is stale even if its
// last sweep "succeeded" — the cron runs hourly) and the unhealthy roll-up live HERE.
//
// health.py's CloudWatch sections (msg="alert" records, the "token source disabled" startup line) are an
// `aws logs` concern the DB-backed endpoint deliberately doesn't carry — see internal/api/health.go. The
// footer notes that gap so an operator knows to also check logs for those.
package render

import (
	"fmt"
	"io"
	"time"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// staleHours mirrors health.py STALE_HOURS: a mirror whose last_ok is older than this is stale even in
// state "ok" — the refresh cron runs hourly, so a 6h+ gap means the worker itself stopped.
const staleHours = 6.0

// HealthVerdict is the pure healthy/unhealthy decision over the raw rows — true ⇒ healthy. It's the
// verdict the -o json path needs (which renders no report but still must set the exit code) and the same
// rule Health renders. A mirror is bad when non-ok OR stale (last_ok older than staleHours / never);
// any mailbox in an attention state or dead-lettered run is unhealthy.
func HealthVerdict(h *client.HealthResponse) bool {
	for _, m := range h.Mirrors {
		if m.State != "ok" || m.HoursSinceOK == nil || *m.HoursSinceOK > staleHours {
			return false
		}
	}
	for _, m := range h.Mailboxes {
		if mailboxNeedsAttention(m) {
			return false
		}
	}
	return len(h.DeadLettered) == 0
}

// Health renders the rolled-up health report and returns healthy=false when ANY section is unhealthy
// (a non-ok/stale mirror, watched mailbox, or any dead-lettered run) — the caller maps that to a
// non-zero exit.
func Health(w io.Writer, h *client.HealthResponse) (healthy bool) {
	unhealthy := false

	// 1. mirrors — non-ok state OR stale (last_ok older than staleHours / never synced).
	var bad []client.HealthMirror
	for _, m := range h.Mirrors {
		if m.State != "ok" || m.HoursSinceOK == nil || *m.HoursSinceOK > staleHours {
			bad = append(bad, m)
		}
	}
	_, _ = fmt.Fprintf(w, "Mirrors — %d/%d healthy\n", len(h.Mirrors)-len(bad), len(h.Mirrors))
	if len(bad) > 0 {
		unhealthy = true
		for _, m := range bad {
			_, _ = fmt.Fprintf(w, "  ! %s: state=%s fails=%d last_ok=%s ago — %s\n",
				m.Repo, m.State, m.ConsecutiveFailures, ageOrNever(m.HoursSinceOK), firstLine80(m.LastError))
		}
	} else {
		_, _ = fmt.Fprintln(w, "  ok — all mirrors synced recently")
	}

	// 2. watched mailboxes — raw rows; error/needs_attention or active expired watches need attention.
	var badMailboxes []client.HealthMailbox
	for _, m := range h.Mailboxes {
		if mailboxNeedsAttention(m) {
			badMailboxes = append(badMailboxes, m)
		}
	}
	_, _ = fmt.Fprintf(w, "\nWatched mailboxes — %d/%d healthy\n", len(h.Mailboxes)-len(badMailboxes), len(h.Mailboxes))
	if len(badMailboxes) > 0 {
		unhealthy = true
		for _, m := range badMailboxes {
			line := fmt.Sprintf("  ! %s %s (%s) status=%s expires=%s", m.Provider, m.EmailAddress, mailboxTenant(m.Tenant), m.Status, mailboxExpiry(m))
			if msg := firstLine80(m.ErrorMessage); msg != "" {
				line += " — " + msg
			}
			_, _ = fmt.Fprintln(w, line)
		}
	} else {
		_, _ = fmt.Fprintln(w, "  ok — no watched mailboxes parked or expired")
	}

	// 3. dead-lettered runs — any in the window is unhealthy (the customer never got the draft).
	_, _ = fmt.Fprintf(w, "\nDead-lettered runs (last %dh) — %d total\n", h.WindowHours, len(h.DeadLettered))
	if len(h.DeadLettered) > 0 {
		unhealthy = true
		for _, d := range h.DeadLettered {
			_, _ = fmt.Fprintf(w, "  ! %s %s — %s\n", d.Kind, short8(d.RunID), firstLine120(d.Error))
		}
	} else {
		_, _ = fmt.Fprintln(w, "  ok — no runs dead-lettered in window")
	}

	// The DB surface can't see the CloudWatch alert/config-sanity inputs health.py also checks.
	_, _ = fmt.Fprintln(w, "\nnote: alert + 'token source disabled' log inputs are not in this DB-backed view — check logs (support skill) for those.")

	if unhealthy {
		_, _ = fmt.Fprintln(w, "\nUNHEALTHY")
	} else {
		_, _ = fmt.Fprintln(w, "\nhealthy")
	}
	return !unhealthy
}

// ageOrNever renders an hours-since-ok as "X.Xh", or "never" when the mirror never succeeded.
func ageOrNever(hoursSinceOK *float64) string {
	if hoursSinceOK == nil {
		return "never"
	}
	return fmt.Sprintf("%.1fh", *hoursSinceOK)
}

func mailboxTenant(tenant string) string {
	if tenant == "" {
		return "project"
	}
	return tenant
}

func mailboxNeedsAttention(m client.HealthMailbox) bool {
	switch m.Status {
	case "error", "needs_attention":
		return true
	case "active":
		return expiredTime(m.SubscriptionExpiresAt) || expiredTime(m.SpamSubscriptionExpiresAt)
	default:
		return false
	}
}

func mailboxExpiry(m client.HealthMailbox) string {
	mainExpired := expiredTime(m.SubscriptionExpiresAt)
	spamExpired := expiredTime(m.SpamSubscriptionExpiresAt)
	switch {
	case mainExpired && m.SubscriptionExpiresAt != "":
		return m.SubscriptionExpiresAt
	case spamExpired && m.SpamSubscriptionExpiresAt != "":
		return "spam " + m.SpamSubscriptionExpiresAt
	case m.SubscriptionExpiresAt != "":
		return m.SubscriptionExpiresAt
	case m.SpamSubscriptionExpiresAt != "":
		return "spam " + m.SpamSubscriptionExpiresAt
	default:
		return "-"
	}
}

func expiredTime(s string) bool {
	if s == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, s)
	return err == nil && t.Before(time.Now())
}

func firstLine80(s string) string  { return clipStr(patternsFirstLine(s), 80) }
func firstLine120(s string) string { return clipStr(patternsFirstLine(s), 120) }
