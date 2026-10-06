package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// ProcessedImageLabel names the stored variant of an image whose original was never kept.
const ProcessedImageLabel = "processed (1536px JPEG)"

// Attachments renders `rc run attachments <session>`: every file in the session, oldest first, ending
// in the download command.
func Attachments(w io.Writer, l *client.SessionAttachments) {
	head := "Session " + l.SessionID
	if l.Surface != "" {
		head += " (" + l.Surface + ")"
	}
	if l.TenantSlug != "" {
		head += " · tenant " + l.TenantSlug
	}
	_, _ = fmt.Fprintln(w, head)
	if len(l.Attachments) == 0 {
		_, _ = fmt.Fprintln(w, "\nNo files in this session.")
		return
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "#\tATTACHMENT\tORIGIN\tFILENAME\tSIZE\tMIME\tCREATED\tAVAILABLE\tNOTE")
	for i, a := range l.Attachments {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1, a.AttachmentID, dash(a.Origin),
			dash(a.Filename), humanBytes(a.SizeBytes), dash(a.MimeType), dash(a.CreatedAt),
			availability(a.Available), dash(attachmentNote(a)))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "\nDownload: rc run attachments %s --download  (user uploads; --include-generated adds assistant files)\n", l.SessionID)
}

func attachmentNote(a client.ChatAttachment) string {
	switch {
	case a.ProcessedOnly:
		return ProcessedImageLabel
	case a.Original != nil:
		note := "original " + orDash(a.Original.Filename, "file") + " " + humanBytes(a.Original.SizeBytes)
		if a.Original.Available != nil && !*a.Original.Available {
			note += " (expired)"
		}
		return note
	}
	return ""
}

func availability(b *bool) string {
	if b == nil {
		return "-"
	}
	return yesNo(*b)
}

// ResourceSessionsTable renders the sessions linked to one resource, for the caller to pick one.
func ResourceSessionsTable(w io.Writer, r *client.ResourceSessions) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SESSION\tSURFACE\tLINKED\tLAST ACTIVE\tTITLE")
	for _, s := range r.Sessions {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.SessionID, dash(s.Surface), dash(s.LinkedAt),
			dash(s.LastActiveAt), dash(s.Title))
	}
	_ = tw.Flush()
}

// AttachmentDownload is the local manifest of one `rc run attachments --download`, mapped by
// internal/cli from what it wrote; `-o json` emits it as-is.
type AttachmentDownload struct {
	Files []AttachmentFile `json:"files"`
}

// AttachmentFile is one file's outcome. Status is downloaded|present|expired|failed; Verified means the
// received bytes matched every digest the server supplied (list and download header). Variant is
// "original" or "processed" for images, empty for files stored as uploaded.
type AttachmentFile struct {
	SessionID    string `json:"session_id"`
	AttachmentID string `json:"attachment_id"`
	Origin       string `json:"origin,omitempty"`
	Filename     string `json:"filename"`
	Variant      string `json:"variant,omitempty"`
	Path         string `json:"path,omitempty"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256,omitempty"`
	Verified     bool   `json:"verified"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
}

// AttachmentDownloads renders the manifest table: status first, so a failed or expired file is the
// first thing a reader sees.
func AttachmentDownloads(w io.Writer, d AttachmentDownload) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STATUS\tPATH\tSIZE\tSHA256\tVERIFIED\tNOTE")
	for _, f := range d.Files {
		path := f.Path
		if path == "" {
			path = f.Filename + " (" + f.AttachmentID + ")"
		}
		var notes []string
		if f.Variant == "processed" {
			notes = append(notes, ProcessedImageLabel)
		}
		if f.Message != "" {
			notes = append(notes, f.Message)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", f.Status, path, humanBytes(f.Bytes),
			dash(clipID(f.SHA256, 12)), yesNo(f.Verified), dash(strings.Join(notes, "; ")))
	}
	_ = tw.Flush()
}

// humanBytes formats a byte count in binary units (B, KiB, MiB, GiB).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 2; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMG"[exp])
}

// transcriptFiles appends the session's files to a transcript, with the one command that fetches them.
func transcriptFiles(w io.Writer, sessionID string, files []client.ChatAttachment) {
	if len(files) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "\n## Files\n\n")
	for _, a := range files {
		parts := []string{orDash(a.Filename, "(unnamed)"), humanBytes(a.SizeBytes), a.AttachmentID}
		if a.Origin != "" && a.Origin != client.AttachmentOriginUser {
			parts = append(parts, a.Origin)
		}
		if note := attachmentNote(a); note != "" {
			parts = append(parts, note)
		}
		if a.Available != nil && !*a.Available {
			parts = append(parts, "expired")
		}
		_, _ = fmt.Fprintf(w, "- %s\n", strings.Join(parts, " · "))
	}
	_, _ = fmt.Fprintf(w, "\nDownload: `rc run attachments %s --download`\n", sessionID)
}
