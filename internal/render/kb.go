// Views for `rc project knowledge` content: the collection list, the search summary printed to
// stdout, and the HITS.md written into the artifact directory.

package render

import (
	"fmt"
	"io"
	"strings"
)

// KBHit is one matching line inside an article.
type KBHit struct {
	Line    int
	Snippet string
}

// KBArticle is one matched article, with the hits worth showing.
type KBArticle struct {
	Title string
	URL   string
	Path  string
	Hits  []KBHit
}

// KBCollection is one KB collection and how many articles it holds.
type KBCollection struct {
	Name         string
	ArticleCount int
}

// KBListView is `rc project knowledge list`'s output.
type KBListView struct {
	Project     string
	Provider    string
	Revision    string
	Truncated   bool
	Collections []KBCollection
}

// KBSearchView is the stdout summary after a search: what matched and where the artifacts landed.
type KBSearchView struct {
	ArticlesMatched int
	Hits            int
	ArtifactDir     string
	Truncated       bool
	Articles        []KBArticle
}

// KBHitsView is the HITS.md document written next to the downloaded articles.
type KBHitsView struct {
	Query    string
	Project  string
	Provider string
	Revision string
	HitCount int
	Articles []KBArticle
}

// KBList prints the collections table with the provenance header (project/provider/revision).
func KBList(w io.Writer, v KBListView) {
	if v.Project != "" {
		_, _ = fmt.Fprintf(w, "Project: %s\n", v.Project)
	}
	_, _ = fmt.Fprintf(w, "Provider: %s\n", v.Provider)
	if v.Revision != "" {
		_, _ = fmt.Fprintf(w, "Revision: %s\n", v.Revision)
	}
	if v.Truncated {
		_, _ = fmt.Fprintln(w, "Truncated: true")
	}
	_, _ = fmt.Fprintln(w, "\nCollection\tArticles")
	for _, c := range v.Collections {
		_, _ = fmt.Fprintf(w, "%s\t%d\n", c.Name, c.ArticleCount)
	}
}

// KBSearch prints the search summary: counts, the artifact directory when one was materialised, and
// one block per article with its hit lines.
func KBSearch(w io.Writer, v KBSearchView) {
	_, _ = fmt.Fprintf(w, "Found %d articles", v.ArticlesMatched)
	if v.Hits > 0 {
		_, _ = fmt.Fprintf(w, ", %d matching lines", v.Hits)
	}
	_, _ = fmt.Fprintln(w)
	if v.ArtifactDir != "" {
		_, _ = fmt.Fprintf(w, "Artifacts: %s\n", v.ArtifactDir)
	}
	if v.Truncated {
		_, _ = fmt.Fprintln(w, "Truncated: true")
	}
	for i, article := range v.Articles {
		_, _ = fmt.Fprintf(w, "\n%d. %s\n", i+1, article.Title)
		if article.URL != "" {
			_, _ = fmt.Fprintf(w, "   URL: %s\n", article.URL)
		}
		if v.ArtifactDir != "" {
			_, _ = fmt.Fprintf(w, "   Local: articles/%s\n", article.Path)
		}
		if len(article.Hits) > 0 {
			_, _ = fmt.Fprintf(w, "   Hits: %s\n", hitLineSummary(article.Hits))
		}
	}
}

// KBHitsMarkdown renders HITS.md. It returns a string because the caller writes it into the artifact
// directory, not to stdout.
func KBHitsMarkdown(v KBHitsView, artifactDir string) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "# KB hits: %s\n\n", v.Query)
	_, _ = fmt.Fprintf(&b, "- Project: `%s`\n- Provider: `%s`\n- Artifacts: `%s`\n- Articles: %d\n- Hits: %d\n",
		v.Project, v.Provider, artifactDir, len(v.Articles), v.HitCount)
	if v.Revision != "" {
		_, _ = fmt.Fprintf(&b, "- Revision: `%s`\n", v.Revision)
	}
	for i, article := range v.Articles {
		_, _ = fmt.Fprintf(&b, "\n## %d. %s\n\n", i+1, article.Title)
		if article.URL != "" {
			_, _ = fmt.Fprintf(&b, "- URL: %s\n", article.URL)
		}
		_, _ = fmt.Fprintf(&b, "- Local: `articles/%s`\n", article.Path)
		if len(article.Hits) > 0 {
			_, _ = fmt.Fprintln(&b, "\nHits:")
			for _, hit := range article.Hits {
				_, _ = fmt.Fprintf(&b, "- L%d: %s\n", hit.Line, hit.Snippet)
			}
		}
	}
	return b.String()
}

func hitLineSummary(hits []KBHit) string {
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%d", h.Line))
	}
	return "lines " + strings.Join(lines, ",")
}
