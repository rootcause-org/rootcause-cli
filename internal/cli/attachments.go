package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const (
	attachmentDownloaded = "downloaded"
	attachmentPresent    = "present"
	attachmentExpired    = "expired"
	attachmentFailed     = "failed"

	attachmentExpiredMessage = "chat files are kept 30 days after upload"

	maxNameCandidates = 100
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type attachmentFlags struct {
	resource         string
	allSessions      bool
	download         bool
	ids              []string
	includeGenerated bool
	processed        bool
	dir              string
}

// sessionFiles is one resolved session's attachment list, typed and verbatim.
type sessionFiles struct {
	list *client.SessionAttachments
	raw  json.RawMessage
}

// newRunAttachmentsCmd builds `rc run attachments`: list (default) or download the files in a chat
// session. The session is named by its id, by one of its run ids, or by a resource an executed action
// touched (`--resource kind:id`), which outlives the action params' retention scrub.
func newRunAttachmentsCmd(e *env) *cobra.Command {
	var f attachmentFlags
	cmd := &cobra.Command{
		Use:   "attachments [<session_id|run_id>]",
		Short: "List or download the files in a chat session",
		Long: "List every file in one chat session (user uploads and assistant-generated, any turn), or " +
			"download them with --download (user uploads; --include-generated adds assistant files) or --id " +
			"(repeatable). Name the session by its id or one of its run ids, or with --resource kind:id " +
			"(e.g. support_ticket:<uuid>) for the session whose action touched that resource. Images download " +
			"as originally uploaded unless --processed; legacy images exist only as the processed 1536px JPEG. " +
			"Files stream to .rootcause/attachments/<session_id>/ (override with --dir), are verified against " +
			"the server's sha256 and written 0600. Exits non-zero when any file expired or failed.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if (len(args) == 1) == (f.resource != "") {
				return fmt.Errorf("name exactly one session: a session/run id argument or --resource kind:id")
			}
			if len(args) == 1 && !uuidPattern.MatchString(strings.TrimSpace(args[0])) {
				return fmt.Errorf("want a session or run id (UUID), got %q", args[0])
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			var sessions []sessionFiles
			if f.resource != "" {
				sessions, err = resourceSessionFiles(e, c, f.resource, f.allSessions)
			} else {
				var s sessionFiles
				s, err = sessionOrRunFiles(e, c, strings.TrimSpace(args[0]))
				sessions = []sessionFiles{s}
			}
			if err != nil {
				return withRunAccessHint(err)
			}
			if !f.download && len(f.ids) == 0 {
				return emitSessionFiles(e, sessions)
			}
			return downloadAttachments(e, c, sessions, f)
		},
	}
	cmd.Flags().StringVar(&f.resource, "resource", "", "find the session by a resource an action touched, as kind:id (e.g. support_ticket:<uuid>)")
	cmd.Flags().BoolVar(&f.allSessions, "all-sessions", false, "with --resource: use every linked session instead of requiring exactly one")
	cmd.Flags().BoolVar(&f.download, "download", false, "download the session's user uploads")
	cmd.Flags().StringArrayVar(&f.ids, "id", nil, "download only this attachment_id, any origin (repeatable; implies --download)")
	cmd.Flags().BoolVar(&f.includeGenerated, "include-generated", false, "with --download: also download assistant-generated files")
	cmd.Flags().BoolVar(&f.processed, "processed", false, "download an image's processed 1536px JPEG instead of its original")
	cmd.Flags().StringVar(&f.dir, "dir", "", "download directory (default .rootcause/attachments/<session_id>)")
	return cmd
}

// sessionOrRunFiles tries the id as a session first, then as a run whose session it lists.
func sessionOrRunFiles(e *env, c *client.Client, id string) (sessionFiles, error) {
	project, tenant := e.scopeProject(), e.scopeTenant()
	list, raw, err := c.SessionAttachments(e.ctx(), project, tenant, id)
	var apiErr *client.APIError
	if err == nil || !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		return sessionFiles{list, raw}, err
	}
	run, runErr := c.Run(e.ctx(), id, project, tenant)
	if runErr != nil {
		return sessionFiles{}, err
	}
	if run.SessionID == "" {
		return sessionFiles{}, fmt.Errorf("run %s has no chat session", id)
	}
	list, raw, err = c.SessionAttachments(e.ctx(), project, tenant, run.SessionID)
	return sessionFiles{list, raw}, err
}

// resourceSessionFiles resolves --resource kind:id. More than one linked session is ambiguous: the
// candidates are printed and the caller must pick one (or opt into --all-sessions).
func resourceSessionFiles(e *env, c *client.Client, ref string, all bool) ([]sessionFiles, error) {
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || kind == "" || id == "" {
		return nil, fmt.Errorf("--resource must be kind:id (e.g. support_ticket:<uuid>), got %q", ref)
	}
	project, tenant := e.scopeProject(), e.scopeTenant()
	rs, raw, err := c.ResourceSessions(e.ctx(), project, tenant, kind, id)
	if err != nil {
		return nil, err
	}
	switch {
	case len(rs.Sessions) == 0:
		return nil, fmt.Errorf("no chat session is linked to %s", ref)
	case len(rs.Sessions) > 1 && !all:
		if render.IsJSON(e.mode(), e.out) {
			if err := render.JSON(e.out, raw); err != nil {
				return nil, err
			}
		} else {
			render.ResourceSessionsTable(e.out, rs)
		}
		_, _ = fmt.Fprintf(e.err, "%d sessions are linked to %s: pass one session id, or --all-sessions\n", len(rs.Sessions), ref)
		return nil, &commandError{code: exitUsage, name: "AMBIGUOUS_RESOURCE", silent: true, message: "resource links several sessions"}
	}
	out := make([]sessionFiles, 0, len(rs.Sessions))
	for _, s := range rs.Sessions {
		list, raw, err := c.SessionAttachments(e.ctx(), project, tenant, s.SessionID)
		if err != nil {
			return nil, err
		}
		out = append(out, sessionFiles{list, raw})
	}
	return out, nil
}

// emitSessionFiles lists: one session's body verbatim in JSON, several as a JSON array of them.
func emitSessionFiles(e *env, sessions []sessionFiles) error {
	if render.IsJSON(e.mode(), e.out) {
		if len(sessions) == 1 {
			return e.renderJSON("attachments-"+shortRunID(sessions[0].list.SessionID), sessions[0].raw)
		}
		raws := make([]json.RawMessage, 0, len(sessions))
		for _, s := range sessions {
			raws = append(raws, s.raw)
		}
		body, err := json.Marshal(raws)
		if err != nil {
			return err
		}
		return e.renderJSON("attachments", body)
	}
	for i, s := range sessions {
		if i > 0 {
			_, _ = fmt.Fprintln(e.out)
		}
		render.Attachments(e.out, s.list)
	}
	return nil
}

type attachmentJob struct {
	sessionID, dir string
	file           client.ChatAttachment
}

// selectAttachmentJobs picks what to download: the named --id files (any origin), else every user
// upload plus, with --include-generated, the assistant's files.
func selectAttachmentJobs(sessions []sessionFiles, f attachmentFlags) ([]attachmentJob, error) {
	want := map[string]bool{}
	for _, id := range f.ids {
		want[strings.ToLower(strings.TrimSpace(id))] = false
	}
	var jobs []attachmentJob
	for _, s := range sessions {
		dir := f.dir
		switch {
		case dir == "":
			dir = filepath.Join(".rootcause", "attachments", s.list.SessionID)
		case len(sessions) > 1:
			dir = filepath.Join(dir, s.list.SessionID)
		}
		for _, a := range s.list.Attachments {
			id := strings.ToLower(a.AttachmentID)
			if _, named := want[id]; named {
				want[id] = true
			} else if len(want) > 0 || (a.Origin != client.AttachmentOriginUser && !f.includeGenerated) {
				continue
			}
			jobs = append(jobs, attachmentJob{sessionID: s.list.SessionID, dir: dir, file: a})
		}
	}
	for id, found := range want {
		if !found {
			return nil, fmt.Errorf("attachment %s is not in this session (list them without --id)", id)
		}
	}
	return jobs, nil
}

func downloadAttachments(e *env, c *client.Client, sessions []sessionFiles, f attachmentFlags) error {
	jobs, err := selectAttachmentJobs(sessions, f)
	if err != nil {
		return err
	}
	manifest := render.AttachmentDownload{Files: make([]render.AttachmentFile, 0, len(jobs))}
	incomplete := 0
	for _, j := range jobs {
		if err := os.MkdirAll(j.dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", j.dir, err)
		}
		r := downloadAttachment(e, c, j, f.processed)
		if r.Status == attachmentExpired || r.Status == attachmentFailed {
			incomplete++
		}
		manifest.Files = append(manifest.Files, r)
	}
	if render.IsJSON(e.mode(), e.out) {
		body, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		if err := render.JSON(e.out, body); err != nil {
			return err
		}
	} else if len(jobs) == 0 {
		_, _ = fmt.Fprintln(e.out, "No user uploads to download (--include-generated adds assistant files).")
	} else {
		render.AttachmentDownloads(e.out, manifest)
	}
	if incomplete > 0 {
		_, _ = fmt.Fprintf(e.err, "%d of %d files not downloaded\n", incomplete, len(jobs))
		return &commandError{code: exitRemote, name: "INCOMPLETE", silent: true, message: "some attachments were not downloaded"}
	}
	return nil
}

// downloadAttachment fetches one file into its dir. Images default to the as-uploaded original when
// the server still has it. The local name is the sanitized filename; an existing file with the same
// digest is reported present (no transfer), a different one gets the attachment id appended to the
// stem so two uploads named alike never overwrite each other.
func downloadAttachment(e *env, c *client.Client, j attachmentJob, processed bool) render.AttachmentFile {
	a := j.file
	r := render.AttachmentFile{SessionID: j.sessionID, AttachmentID: a.AttachmentID, Origin: a.Origin,
		Filename: a.Filename, Bytes: a.SizeBytes, SHA256: a.SHA256}
	variant, available := "", a.Available
	switch o := a.Original; {
	case o != nil && !processed && (o.Available == nil || *o.Available):
		variant, available, r.Variant = client.AttachmentVariantOriginal, o.Available, "original"
		r.Bytes, r.SHA256 = o.SizeBytes, o.SHA256
		if o.Filename != "" {
			r.Filename = o.Filename
		}
	case o != nil || a.ProcessedOnly:
		r.Variant = "processed"
		if o != nil && !processed {
			r.Message = "original no longer stored"
		}
	}
	if available != nil && !*available {
		r.Status, r.Message = attachmentExpired, attachmentExpiredMessage
		return r
	}

	name := sanitizeAttachmentName(r.Filename, a.AttachmentID)
	candidate := func(i int) string {
		ext := filepath.Ext(name)
		switch i {
		case 0:
			return filepath.Join(j.dir, name)
		case 1:
			return filepath.Join(j.dir, strings.TrimSuffix(name, ext)+"-"+shortRunID(a.AttachmentID)+ext)
		}
		return filepath.Join(j.dir, fmt.Sprintf("%s-%s-%d%s", strings.TrimSuffix(name, ext), shortRunID(a.AttachmentID), i, ext))
	}
	// Generated files are detokenized on the way out, so their listed digest (of the stored, tokenized
	// bytes) never matches what arrives: verify those against the bytes only.
	if a.Origin != client.AttachmentOriginUser {
		r.SHA256 = ""
	}
	// First free name; an existing file with the same digest is this upload already downloaded.
	free := 0
	for ; r.SHA256 != "" && free < maxNameCandidates; free++ {
		same, exists := fileHasSHA(candidate(free), r.SHA256)
		if same {
			r.Path, r.Status, r.Verified = candidate(free), attachmentPresent, true
			return r
		}
		if !exists {
			break
		}
	}

	fail := func(err error) render.AttachmentFile {
		r.Status, r.Message = attachmentFailed, err.Error()
		return r
	}
	tmp, err := os.CreateTemp(j.dir, ".rc-attachment-*")
	if err != nil {
		return fail(err)
	}
	// The temp name is only ever hard-linked into place, so it is always removed.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	h := sha256.New()
	counter := &byteCounter{}
	headerSHA, err := c.DownloadAttachment(e.ctx(), e.scopeProject(), e.scopeTenant(), j.sessionID, a.AttachmentID, variant, io.MultiWriter(tmp, h, counter))
	if err != nil {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusGone || apiErr.Code == client.AttachmentExpiredCode) {
			r.Status, r.Message = attachmentExpired, attachmentExpiredMessage
			return r
		}
		return fail(err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	for _, want := range []string{r.SHA256, headerSHA} {
		if want != "" && !strings.EqualFold(want, got) {
			return fail(fmt.Errorf("sha256 mismatch: got %s, server says %s", got, strings.ToLower(want)))
		}
	}
	r.Verified = r.SHA256 != "" || headerSHA != ""
	r.Bytes, r.SHA256 = counter.n, got
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	// Link never replaces: a name taken since the scan (or a local edit) moves on to the next one.
	for i := free; i < maxNameCandidates; i++ {
		path := candidate(i)
		err := os.Link(tmp.Name(), path)
		if errors.Is(err, fs.ErrExist) {
			if same, _ := fileHasSHA(path, r.SHA256); same {
				r.Path, r.Status = path, attachmentPresent
				return r
			}
			continue
		}
		if err != nil {
			return fail(err)
		}
		r.Path, r.Status = path, attachmentDownloaded
		return r
	}
	return fail(fmt.Errorf("no free file name for %s in %s", name, j.dir))
}

type byteCounter struct{ n int64 }

func (b *byteCounter) Write(p []byte) (int, error) {
	b.n += int64(len(p))
	return len(p), nil
}

// fileHasSHA reports whether path exists and, if so, whether its bytes hash to want. A missing digest
// never matches, so an unverifiable existing file is never mistaken for the upload.
func fileHasSHA(path, want string) (same, exists bool) {
	fh, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer func() { _ = fh.Close() }()
	if want == "" {
		return false, true
	}
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return false, true
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), want), true
}

// sanitizeAttachmentName reduces an uploader-chosen filename to one safe path component: no directory
// parts, no control characters, no leading dot (hidden file / "..").
func sanitizeAttachmentName(name, attachmentID string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ':' {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		return shortRunID(attachmentID)
	}
	return name
}
