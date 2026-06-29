package cli

import (
	"github.com/spf13/cobra"
)

// newMailboxCmd: `rc mailbox ls/add` over the mailboxes collection (id = mailbox_id uuid). The server
// owns the schema (the resource auto-exists in /meta/schema); create is an upsert keyed on the email,
// so `add` doubles as edit. No set/rm — the server exposes list+create only.
func newMailboxCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{Use: "mailbox", Short: "Manage a tenant's inbound mailboxes (list + upsert)"}
	cmd.AddCommand(
		listSubCmd(e, "mailboxes"),
		addSubCmd(e, "mailboxes"),
	)
	return cmd
}
