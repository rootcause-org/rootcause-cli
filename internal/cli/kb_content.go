package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const defaultKBLimit = 25

// The KB provider mount is never guessed: with no --provider we discover every mount under /kb in
// the same round-trip as the query itself (see kbPythonPrelude).
const kbProviderFlagHelp = "KB provider mount under /kb (default: all mounted providers)"

type kbListResponse struct {
	Project     string              `json:"project,omitempty"`
	Provider    string              `json:"provider,omitempty"`
	Root        string              `json:"root,omitempty"`
	Revision    string              `json:"revision,omitempty"`
	Collections []kbCollection      `json:"collections"`
	Providers   []kbProviderListing `json:"providers,omitempty"`
	Truncated   bool                `json:"truncated"`
}

// kbProviderListing is one discovered /kb mount. The top-level provider/root/revision/collections
// fields stay populated for the single-provider case so existing consumers keep working.
type kbProviderListing struct {
	Provider    string         `json:"provider"`
	Root        string         `json:"root"`
	Revision    string         `json:"revision,omitempty"`
	Collections []kbCollection `json:"collections"`
}

type kbCollection struct {
	Name         string `json:"name"`
	ArticleCount int    `json:"article_count"`
}

type kbSearchResponse struct {
	Project      string      `json:"project,omitempty"`
	Provider     string      `json:"provider,omitempty"`
	Providers    []string    `json:"providers,omitempty"`
	Query        string      `json:"query,omitempty"`
	Revision     string      `json:"revision,omitempty"`
	ArticleCount int         `json:"article_count"`
	HitCount     int         `json:"hit_count"`
	Truncated    bool        `json:"truncated"`
	Articles     []kbArticle `json:"articles"`
}

type kbArticle struct {
	ID         string  `json:"id,omitempty"`
	Title      string  `json:"title"`
	URL        string  `json:"url,omitempty"`
	Provider   string  `json:"provider,omitempty"`
	Collection string  `json:"collection,omitempty"`
	Path       string  `json:"path"` // /kb-relative: <provider>/<collection>/...
	Score      int     `json:"score,omitempty"`
	Hits       []kbHit `json:"hits,omitempty"`
}

type kbHit struct {
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

type kbArtifactManifest struct {
	Project          string      `json:"project,omitempty"`
	Provider         string      `json:"provider,omitempty"`
	Query            string      `json:"query,omitempty"`
	CreatedAt        string      `json:"created_at"`
	RCVersion        string      `json:"rc_version"`
	SourceKBRevision string      `json:"source_kb_revision,omitempty"`
	ArticleCount     int         `json:"article_count"`
	HitCount         int         `json:"hit_count"`
	Truncated        bool        `json:"truncated"`
	CommandArgs      []string    `json:"command_args,omitempty"`
	Articles         []kbArticle `json:"articles"`
}

type kbCommandSummary struct {
	Project         string      `json:"project,omitempty"`
	Provider        string      `json:"provider,omitempty"`
	Query           string      `json:"query,omitempty"`
	ArtifactDir     string      `json:"artifact_dir,omitempty"`
	ArticlesMatched int         `json:"articles_matched"`
	Hits            int         `json:"hits,omitempty"`
	Truncated       bool        `json:"truncated"`
	Articles        []kbArticle `json:"articles,omitempty"`
}

func newKBListCmd(e *env) *cobra.Command {
	var provider, collection string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List KB collections without article bodies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, err := runKBList(e, c, provider, collection, limit)
			if err != nil {
				return err
			}
			if resp.Project == "" {
				resp.Project = e.scopeProject()
			}
			if e.jsonOut() {
				return writeJSON(e, resp)
			}
			for i, view := range kbListViews(resp) {
				if i > 0 {
					fmt.Fprintln(e.out)
				}
				render.KBList(e.out, view)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", kbProviderFlagHelp)
	cmd.Flags().StringVar(&collection, "collection", "", "limit inventory to one provider-relative collection")
	cmd.Flags().IntVar(&limit, "limit", 0, "max collections to print")
	return cmd
}

func newKBSearchCmd(e *env, version string) *cobra.Command {
	var provider, out string
	var limit int
	var noMaterialize bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search KB articles and write matched articles to local artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := e.newClient()
			if err != nil {
				return err
			}
			resp, err := runKBSearch(e, c, args[0], provider, limit)
			if err != nil {
				return err
			}
			if resp.Project == "" {
				resp.Project = e.scopeProject()
			}
			summary, err := finishKBArtifacts(e, c, version, resp, out, !noMaterialize, cmd.Flags().Args())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return writeJSON(e, summary)
			}
			render.KBSearch(e.out, kbSearchView(summary))
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", kbProviderFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", defaultKBLimit, "max matched articles to materialize")
	cmd.Flags().StringVar(&out, "out", "", "artifact directory (must not already exist)")
	cmd.Flags().BoolVar(&noMaterialize, "no-materialize", false, "skip writing full article markdown files")
	return cmd
}

func newKBExportCmd(e *env, version string) *cobra.Command {
	var provider, query, article, out string
	var all bool
	cmd := &cobra.Command{
		Use:   "export (--query <query> | --article <id-or-path> | --all)",
		Short: "Export selected KB articles to a fresh local artifact directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected := 0
			for _, ok := range []bool{query != "", article != "", all} {
				if ok {
					selected++
				}
			}
			if selected != 1 {
				return fmt.Errorf("choose exactly one of --query, --article, or --all")
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			var resp *kbSearchResponse
			switch {
			case query != "":
				resp, err = runKBSearch(e, c, query, provider, 0)
			case article != "":
				resp, err = runKBArticleSelect(e, c, article, provider)
			default:
				resp, err = runKBAll(e, c, provider)
			}
			if err != nil {
				return err
			}
			if resp.Project == "" {
				resp.Project = e.scopeProject()
			}
			summary, err := finishKBArtifacts(e, c, version, resp, out, true, cmd.Flags().Args())
			if err != nil {
				return err
			}
			if e.jsonOut() {
				return writeJSON(e, summary)
			}
			render.KBSearch(e.out, kbSearchView(summary))
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", kbProviderFlagHelp)
	cmd.Flags().StringVar(&query, "query", "", "search query to export")
	cmd.Flags().StringVar(&article, "article", "", "article id, filename, or provider-relative path to export")
	cmd.Flags().BoolVar(&all, "all", false, "export all provider articles")
	cmd.Flags().StringVar(&out, "out", "", "artifact directory (must not already exist)")
	return cmd
}

func runKBList(e *env, c *client.Client, provider, collection string, limit int) (*kbListResponse, error) {
	if err := validateKBProvider(provider); err != nil {
		return nil, err
	}
	resp, err := runKBScript(e, c, kbListScript(provider, collection, limit), 60)
	if err != nil {
		return nil, err
	}
	var out kbListResponse
	if err := json.Unmarshal([]byte(resp.Stdout), &out); err != nil {
		return nil, fmt.Errorf("decode kb list response: %w", err)
	}
	out.Project = firstNonEmpty(out.Project, resp.Project)
	normalizeKBListProviders(&out, provider)
	out.Truncated = out.Truncated || resp.StdoutTruncated || resp.StderrTruncated
	return &out, nil
}

func runKBSearch(e *env, c *client.Client, query, provider string, limit int) (*kbSearchResponse, error) {
	if err := validateKBProvider(provider); err != nil {
		return nil, err
	}
	resp, err := runKBScript(e, c, kbSearchScript(query, provider, limit), 90)
	if err != nil {
		return nil, err
	}
	var out kbSearchResponse
	if err := json.Unmarshal([]byte(resp.Stdout), &out); err != nil {
		return nil, fmt.Errorf("decode kb search response: %w", err)
	}
	out.Project = firstNonEmpty(out.Project, resp.Project)
	out.Provider = firstNonEmpty(out.Provider, strings.Join(out.Providers, ", "), provider)
	out.Query = query
	out.Truncated = out.Truncated || resp.StdoutTruncated || resp.StderrTruncated
	return &out, nil
}

func runKBArticleSelect(e *env, c *client.Client, article, provider string) (*kbSearchResponse, error) {
	if err := validateKBProvider(provider); err != nil {
		return nil, err
	}
	resp, err := runKBScript(e, c, kbArticleSelectScript(article, provider), 60)
	if err != nil {
		return nil, err
	}
	var out kbSearchResponse
	if err := json.Unmarshal([]byte(resp.Stdout), &out); err != nil {
		return nil, fmt.Errorf("decode kb article response: %w", err)
	}
	out.Project = firstNonEmpty(out.Project, resp.Project)
	out.Provider = firstNonEmpty(out.Provider, strings.Join(out.Providers, ", "), provider)
	out.Query = article
	out.Truncated = out.Truncated || resp.StdoutTruncated || resp.StderrTruncated
	return &out, nil
}

func runKBAll(e *env, c *client.Client, provider string) (*kbSearchResponse, error) {
	if err := validateKBProvider(provider); err != nil {
		return nil, err
	}
	resp, err := runKBScript(e, c, kbAllScript(provider), 90)
	if err != nil {
		return nil, err
	}
	var out kbSearchResponse
	if err := json.Unmarshal([]byte(resp.Stdout), &out); err != nil {
		return nil, fmt.Errorf("decode kb export response: %w", err)
	}
	out.Project = firstNonEmpty(out.Project, resp.Project)
	out.Provider = firstNonEmpty(out.Provider, strings.Join(out.Providers, ", "), provider)
	out.Truncated = out.Truncated || resp.StdoutTruncated || resp.StderrTruncated
	return &out, nil
}

func runKBScript(e *env, c *client.Client, script string, timeout int) (*client.BashRunResponse, error) {
	resp, _, err := c.BashRun(e.ctx(), client.BashRunRequest{Command: script, TimeoutS: timeout}, e.scopeProject(), e.scopeTenant())
	if err != nil {
		return nil, err
	}
	if resp.ExitCode != 0 {
		return nil, fmt.Errorf("kb remote command failed: exit %d: %s", resp.ExitCode, strings.TrimSpace(resp.Stderr))
	}
	if resp.TimedOut {
		return nil, fmt.Errorf("kb remote command timed out")
	}
	return resp, nil
}

func finishKBArtifacts(e *env, c *client.Client, version string, resp *kbSearchResponse, out string, materialize bool, args []string) (*kbCommandSummary, error) {
	summary := &kbCommandSummary{
		Project:         resp.Project,
		Provider:        resp.Provider,
		Query:           resp.Query,
		ArticlesMatched: len(resp.Articles),
		Hits:            resp.HitCount,
		Truncated:       resp.Truncated,
		Articles:        resp.Articles,
	}
	if !materialize {
		return summary, nil
	}
	dir, display, err := kbArtifactDir(out, resp.Query)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "articles"), 0o755); err != nil {
		return nil, fmt.Errorf("create kb artifact dir: %w", err)
	}
	for _, article := range resp.Articles {
		body, truncated, err := fetchKBArticle(e, c, article.Path)
		if err != nil {
			return nil, err
		}
		if truncated {
			summary.Truncated = true
		}
		path := filepath.Join(dir, "articles", filepath.FromSlash(cleanKBRelPath(article.Path)))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create article dir: %w", err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return nil, fmt.Errorf("write article %s: %w", article.Path, err)
		}
	}
	manifest := kbArtifactManifest{
		Project:          resp.Project,
		Provider:         resp.Provider,
		Query:            resp.Query,
		CreatedAt:        time.Now().Format(time.RFC3339),
		RCVersion:        version,
		SourceKBRevision: resp.Revision,
		ArticleCount:     len(resp.Articles),
		HitCount:         resp.HitCount,
		Truncated:        summary.Truncated,
		CommandArgs:      args,
		Articles:         resp.Articles,
	}
	if err := writeJSONFile(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "hits.md"), []byte(render.KBHitsMarkdown(kbHitsView(resp), display)), 0o644); err != nil {
		return nil, fmt.Errorf("write hits.md: %w", err)
	}
	summary.ArtifactDir = display
	return summary, nil
}

func fetchKBArticle(e *env, c *client.Client, rel string) ([]byte, bool, error) {
	resp, err := runKBScript(e, c, kbCatScript(rel), 60)
	if err != nil {
		return nil, false, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(resp.Stdout))
	if err != nil {
		return nil, resp.StdoutTruncated || resp.StderrTruncated, fmt.Errorf("decode article %s: %w", rel, err)
	}
	return data, resp.StdoutTruncated || resp.StderrTruncated, nil
}

// The kb* response types stay here: they are the JSON contract of `-o json` and of the artifact
// manifest. These map only the fields the human/markdown views print.

// kbListViews renders one block per discovered provider mount.
func kbListViews(resp *kbListResponse) []render.KBListView {
	views := make([]render.KBListView, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		view := render.KBListView{
			Project: resp.Project, Provider: p.Provider,
			Revision: p.Revision, Truncated: resp.Truncated,
		}
		for _, c := range p.Collections {
			view.Collections = append(view.Collections, render.KBCollection{Name: c.Name, ArticleCount: c.ArticleCount})
		}
		views = append(views, view)
	}
	return views
}

// normalizeKBListProviders keeps the single-provider top-level fields and the providers[] list in
// sync whichever side of the wire filled them in.
func normalizeKBListProviders(out *kbListResponse, requested string) {
	if len(out.Providers) == 0 {
		name := firstNonEmpty(out.Provider, requested)
		out.Providers = []kbProviderListing{{
			Provider: name, Root: firstNonEmpty(out.Root, "/kb/"+name),
			Revision: out.Revision, Collections: out.Collections,
		}}
	}
	names := make([]string, 0, len(out.Providers))
	for _, p := range out.Providers {
		names = append(names, p.Provider)
	}
	out.Provider = firstNonEmpty(out.Provider, strings.Join(names, ", "), requested)
	if len(out.Providers) == 1 {
		out.Root = firstNonEmpty(out.Root, out.Providers[0].Root)
		out.Revision = firstNonEmpty(out.Revision, out.Providers[0].Revision)
		if len(out.Collections) == 0 {
			out.Collections = out.Providers[0].Collections
		}
	}
	if out.Collections == nil {
		out.Collections = []kbCollection{}
	}
}

func kbSearchView(summary *kbCommandSummary) render.KBSearchView {
	return render.KBSearchView{
		ArticlesMatched: summary.ArticlesMatched, Hits: summary.Hits,
		ArtifactDir: summary.ArtifactDir, Truncated: summary.Truncated,
		Articles: kbArticleViews(summary.Articles),
	}
}

func kbHitsView(resp *kbSearchResponse) render.KBHitsView {
	return render.KBHitsView{
		Query: resp.Query, Project: resp.Project, Provider: resp.Provider,
		Revision: resp.Revision, HitCount: resp.HitCount,
		Articles: kbArticleViews(resp.Articles),
	}
}

func kbArticleViews(articles []kbArticle) []render.KBArticle {
	views := make([]render.KBArticle, 0, len(articles))
	for _, a := range articles {
		view := render.KBArticle{Title: a.Title, URL: a.URL, Path: a.Path}
		for _, h := range a.Hits {
			view.Hits = append(view.Hits, render.KBHit{Line: h.Line, Snippet: h.Snippet})
		}
		views = append(views, view)
	}
	return views
}

func kbArtifactDir(out, query string) (string, string, error) {
	var dir string
	if out != "" {
		dir = out
	} else {
		dir = uniqueKBArtifactDir(query)
	}
	if _, err := os.Stat(dir); err == nil {
		return "", "", fmt.Errorf("artifact directory already exists: %s", dir)
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	display := dir
	if rel, err := filepath.Rel(mustGetwd(), abs); err == nil && !strings.HasPrefix(rel, "..") {
		display = filepath.ToSlash(rel)
	}
	return abs, display, nil
}

func uniqueKBArtifactDir(query string) string {
	base := filepath.Join(".rootcause", "tmp", "kb-searches", time.Now().Format("20060102-150405")+"-"+querySlug(query))
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%02d", base, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// An empty provider means "every mounted provider" and is resolved remotely, not here.
func validateKBProvider(provider string) error {
	if provider == "" {
		return nil
	}
	if provider != filepath.Base(provider) || strings.Contains(provider, "..") || strings.ContainsAny(provider, `/\`) {
		return fmt.Errorf("invalid --provider %q (must be a simple provider name under /kb)", provider)
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func querySlug(query string) string {
	s := strings.ToLower(query)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "kb"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

func cleanKBRelPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(path, "/")))
	path = strings.TrimPrefix(path, "../")
	if path == "." || path == "" {
		return "article.md"
	}
	return path
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func kbListScript(provider, collection string, limit int) string {
	return kbPython(map[string]any{"provider": provider, "collection": collection, "limit": limit}, `
# rc-kb:list
import json, os
collection_filter = (ARGS.get("collection") or "").strip("/")
limit = int(ARGS.get("limit") or 0)
providers = kb_providers(ARGS.get("provider") or "")
listings, truncated = [], False
for provider in providers:
    root = os.path.join("/kb", provider)
    counts = {}
    for dirpath, _, filenames in os.walk(root):
        for name in filenames:
            if not name.endswith(".md"):
                continue
            rel = os.path.relpath(os.path.join(dirpath, name), root).replace(os.sep, "/")
            if collection_filter and not rel.startswith(collection_filter + "/") and rel != collection_filter:
                continue
            collection = rel.split("/", 1)[0] if "/" in rel else "."
            counts[collection] = counts.get(collection, 0) + 1
    items = [{"name": k, "article_count": counts[k]} for k in sorted(counts)]
    if limit > 0 and len(items) > limit:
        items = items[:limit]
        truncated = True
    listings.append({"provider": provider, "root": root, "revision": kb_revision(root), "collections": items})
payload = {"providers": listings, "truncated": truncated}
if len(listings) == 1:
    payload.update({k: listings[0][k] for k in ("provider", "root", "revision", "collections")})
print(json.dumps(payload, ensure_ascii=False))
`)
}

func kbSearchScript(query, provider string, limit int) string {
	return kbPython(map[string]any{"provider": provider, "query": query, "limit": limit}, `
# rc-kb:search
import json, os, re
query = ARGS.get("query") or ""
limit = int(ARGS.get("limit") or 0)
providers = kb_providers(ARGS.get("provider") or "")
try:
    rx = re.compile(query, re.I)
except re.error:
    rx = re.compile(re.escape(query), re.I)

def meta_and_title(text, fallback):
    meta, title = {}, ""
    lines = text.splitlines()
    if lines and lines[0].strip() == "---":
        for line in lines[1:]:
            if line.strip() == "---":
                break
            if ":" in line:
                k, v = line.split(":", 1)
                meta[k.strip().lower()] = v.strip().strip('"')
    title = meta.get("title") or ""
    if not title:
        for line in lines:
            if line.startswith("# "):
                title = line[2:].strip()
                break
    return meta, title or fallback

articles, total_hits = [], 0
for provider in providers:
    root = os.path.join("/kb", provider)
    for dirpath, _, filenames in os.walk(root):
        for name in sorted(filenames):
            if not name.endswith(".md"):
                continue
            full = os.path.join(dirpath, name)
            rel = kb_rel(full)
            try:
                text = open(full, encoding="utf-8", errors="replace").read()
            except Exception:
                continue
            meta, title = meta_and_title(text, os.path.splitext(name)[0])
            hay = "\n".join([title, meta.get("summary", ""), meta.get("keywords", ""), meta.get("url", ""), text])
            if not rx.search(hay):
                continue
            hits = []
            article_hit_count = 0
            for lineno, line in enumerate(text.splitlines(), 1):
                if rx.search(line):
                    article_hit_count += 1
                    if len(hits) < 8:
                        hits.append({"line": lineno, "snippet": line.strip()[:220]})
            if not hits:
                hits.append({"line": 0, "snippet": "match in title/metadata"})
                article_hit_count = 1
            total_hits += article_hit_count
            score = article_hit_count
            if rx.search(title): score += 5
            if rx.search(meta.get("summary", "")): score += 3
            if rx.search(meta.get("keywords", "")): score += 2
            articles.append({
                "id": meta.get("article_id") or meta.get("id") or os.path.splitext(name)[0].split("-", 1)[0],
                "title": title,
                "url": meta.get("url", ""),
                "provider": provider,
                "collection": kb_collection(rel),
                "path": rel,
                "score": score,
                "hits": hits,
            })
articles.sort(key=lambda a: (-a.get("score", 0), a.get("title", ""), a.get("path", "")))
truncated = False
if limit > 0 and len(articles) > limit:
    articles = articles[:limit]
    truncated = True
print(json.dumps(kb_result(providers, articles, query=query, hit_count=total_hits, truncated=truncated), ensure_ascii=False))
`)
}

func kbArticleSelectScript(article, provider string) string {
	return kbPython(map[string]any{"provider": provider, "article": article}, `
# rc-kb:article
import json, os
needle = (ARGS.get("article") or "").strip("/")
providers = kb_providers(ARGS.get("provider") or "")
items = []

def meta_title(text, fallback):
    meta, title = {}, ""
    lines = text.splitlines()
    if lines and lines[0].strip() == "---":
        for line in lines[1:]:
            if line.strip() == "---": break
            if ":" in line:
                k, v = line.split(":", 1); meta[k.strip().lower()] = v.strip().strip('"')
    for line in lines:
        if line.startswith("# "):
            title = line[2:].strip(); break
    return meta, meta.get("title") or title or fallback

for provider in providers:
    root = os.path.join("/kb", provider)
    for dirpath, _, filenames in os.walk(root):
        for name in sorted(filenames):
            if not name.endswith(".md"): continue
            full = os.path.join(dirpath, name)
            rel = kb_rel(full)
            if needle not in rel and needle != os.path.splitext(name)[0].split("-", 1)[0]:
                continue
            text = open(full, encoding="utf-8", errors="replace").read()
            meta, title = meta_title(text, os.path.splitext(name)[0])
            items.append({"id": meta.get("article_id") or meta.get("id") or os.path.splitext(name)[0].split("-", 1)[0], "title": title, "url": meta.get("url", ""), "provider": provider, "collection": kb_collection(rel), "path": rel, "hits": []})
print(json.dumps(kb_result(providers, items, query=needle), ensure_ascii=False))
`)
}

func kbAllScript(provider string) string {
	return kbPython(map[string]any{"provider": provider}, `
# rc-kb:all
import json, os
providers = kb_providers(ARGS.get("provider") or "")
items = []
for provider in providers:
    root = os.path.join("/kb", provider)
    for dirpath, _, filenames in os.walk(root):
        for name in sorted(filenames):
            if not name.endswith(".md"): continue
            rel = kb_rel(os.path.join(dirpath, name))
            items.append({"id": os.path.splitext(name)[0].split("-", 1)[0], "title": os.path.splitext(name)[0], "provider": provider, "collection": kb_collection(rel), "path": rel, "hits": []})
print(json.dumps(kb_result(providers, items), ensure_ascii=False))
`)
}

// kbCatScript resolves a /kb-relative path (<provider>/<collection>/…), so an article row from a
// multi-provider search is fetchable without carrying its provider separately.
func kbCatScript(rel string) string {
	return kbPython(map[string]any{"path": cleanKBRelPath(rel)}, `
# rc-kb:cat
import base64, os, sys
rel = (ARGS.get("path") or "").strip("/")
root = os.path.realpath("/kb")
path = os.path.realpath(os.path.join(root, rel))
if not path.startswith(root + os.sep) or not os.path.isfile(path):
    print("article not found", file=sys.stderr)
    sys.exit(1)
with open(path, "rb") as f:
    sys.stdout.write(base64.b64encode(f.read()).decode("ascii"))
`)
}

// kbPythonPrelude is shared by every remote KB script: provider discovery (the top-level non-dot
// directories under /kb are the mounts) plus the /kb-relative path helpers, so a run never guesses a
// provider and never needs a second round-trip to find out which ones are mounted.
const kbPythonPrelude = `
import os as _os, subprocess as _sp, sys as _sys

def kb_providers(requested):
    try:
        names = sorted(n for n in _os.listdir("/kb")
                       if not n.startswith(".") and n != "_assets" and _os.path.isdir(_os.path.join("/kb", n)))
    except OSError:
        names = []
    if requested:
        if requested not in names:
            print("provider %s is not mounted under /kb (mounted: %s)" % (requested, ", ".join(names) or "none"), file=_sys.stderr)
            _sys.exit(1)
        return [requested]
    if not names:
        print("no KB provider mounts found under /kb", file=_sys.stderr)
        _sys.exit(1)
    return names

def kb_rel(full):
    return _os.path.relpath(full, "/kb").replace(_os.sep, "/")

def kb_collection(rel):
    parts = rel.split("/")
    return parts[1] if len(parts) > 2 else "."

def kb_revision(root):
    try:
        return _sp.check_output(["git", "-C", root, "rev-parse", "--short=12", "HEAD"], text=True, stderr=_sp.DEVNULL).strip()
    except Exception:
        return ""

def kb_result(providers, articles, query=None, hit_count=0, truncated=False):
    out = {"providers": providers, "revision": kb_revision(_os.path.join("/kb", providers[0])) if len(providers) == 1 else "",
           "article_count": len(articles), "hit_count": hit_count, "truncated": truncated, "articles": articles}
    if len(providers) == 1:
        out["provider"] = providers[0]
    if query is not None:
        out["query"] = query
    return out
`

func kbPython(args map[string]any, body string) string {
	b, _ := json.Marshal(args)
	return "python3 - <<'PY'\nimport json\nARGS = json.loads(" + pyString(string(b)) + ")\n" + kbPythonPrelude + body + "\nPY"
}

func pyString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
