package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/outputspill"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

func (e *env) spillConfig() outputspill.Config {
	return outputspill.NewConfig(e.outDir, e.noPreview, e.rawOutput)
}

func (e *env) renderJSON(label string, raw json.RawMessage) error {
	cfg := e.spillConfig()
	if m, err := outputspill.MaybeSpillJSON(cfg, label, raw); err != nil {
		return err
	} else if m != nil {
		return outputspill.WriteManifest(e.out, *m)
	}
	return render.JSON(e.out, raw)
}

func (e *env) renderBytes(label, name string, b []byte, format string) error {
	cfg := e.spillConfig()
	if !cfg.ShouldSpillBytes(b) {
		_, err := e.out.Write(b)
		return err
	}
	art, err := outputspill.WriteArtifact(cfg, cfg.DirFor(label), name, b, format, false)
	if err != nil {
		return err
	}
	if e.jsonOut() {
		return outputspill.WriteManifest(e.out, outputspill.ManifestForArtifact(art))
	}
	return writeSpillPreview(e, art)
}

func writeSpillPreview(e *env, art outputspill.Artifact) error {
	if _, err := fmt.Fprintf(e.out, "[output too large: %d bytes, %d lines - full output saved to %s]\n", art.Bytes, art.Lines, art.Path); err != nil {
		return err
	}
	if art.Preview != nil {
		if art.Preview.Head != "" {
			if _, err := fmt.Fprint(e.out, art.Preview.Head); err != nil {
				return err
			}
			if art.Preview.Head[len(art.Preview.Head)-1] != '\n' {
				if _, err := fmt.Fprintln(e.out); err != nil {
					return err
				}
			}
		}
		if art.Preview.Tail != "" {
			if _, err := fmt.Fprintln(e.out, "...[middle omitted]..."); err != nil {
				return err
			}
			if _, err := fmt.Fprint(e.out, art.Preview.Tail); err != nil {
				return err
			}
			if art.Preview.Tail[len(art.Preview.Tail)-1] != '\n' {
				if _, err := fmt.Fprintln(e.out); err != nil {
					return err
				}
			}
		}
	}
	if len(art.Hints) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(e.out, "\nHints:"); err != nil {
		return err
	}
	for _, h := range art.Hints {
		if _, err := fmt.Fprintf(e.out, "  %s\n", h); err != nil {
			return err
		}
	}
	return nil
}

// treeFile is one payload-supplied file materialised verbatim under an artifact dir's tree/.
type treeFile struct {
	Path    string
	Content string
}

// safeTreePath maps a payload-supplied relative path into root. Absolute paths and any ".." segment are
// rejected: the payload comes from the server and must never be able to write outside the artifact dir.
func safeTreePath(root, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, '\x00') {
		return "", false
	}
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) || strings.HasPrefix(p, string(filepath.Separator)) {
		return "", false
	}
	clean := filepath.Clean(p)
	if clean == "." {
		return "", false
	}
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".." {
			return "", false
		}
	}
	return filepath.Join(root, clean), true
}

// writeFileTree materialises files verbatim under <dir>/tree/ so a reviewer can open the real
// "playbooks/x.md" instead of an opaque content.txt. The previous tree is wiped first: a file that
// disappeared from this render must not survive from the last one. Returns the tree root, the number of
// files written and the paths skipped by safeTreePath.
func writeFileTree(dir string, files []treeFile) (root string, written int, skipped []string, err error) {
	root = filepath.Join(dir, "tree")
	if err := os.RemoveAll(root); err != nil {
		return root, 0, nil, fmt.Errorf("wipe %s: %w", root, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return root, 0, nil, fmt.Errorf("create %s: %w", root, err)
	}
	for _, f := range files {
		path, ok := safeTreePath(root, f.Path)
		if !ok {
			skipped = append(skipped, f.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return root, written, skipped, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o600); err != nil {
			return root, written, skipped, fmt.Errorf("write %s: %w", path, err)
		}
		written++
	}
	return root, written, skipped, nil
}
