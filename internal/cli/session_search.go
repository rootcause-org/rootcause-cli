package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

const sessionSearchMaxPages = 20

func newSessionSearchCmd(e *env) *cobra.Command {
	var kind, id, since, until, contains, surface, before string
	var limit int
	cmd := &cobra.Command{
		Use: "search", Short: "Find chat sessions by principal, creation date and prose",
		Long: "Search authorized chat sessions, newest first, using existing read endpoints. " +
			"Dates constrain SESSION CREATION, not individual turns or last activity. Date-only values use UTC. " +
			"--contains matches a case-insensitive literal in title, opening preview or ordinary message text; " +
			"cards and attachment contents are not searched. Only matching principal/date candidates need a transcript read. " +
			"Scans at most 20 pages of 100 rows per call, with no history age limit. " +
			"Coverage and --before continuation are explicit; -o json retains original matching session rows. " +
			"Read a match with `rc run thread SESSION --transcript`, then `rc run debug RUN`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			principal, err := principalFromFlags(kind, id, "", "")
			if err != nil {
				return err
			}
			start, end, err := digest.SessionSearchWindow(since, until)
			if err != nil {
				return err
			}
			if err := validateSessionSurface(surface); err != nil {
				return err
			}
			if limit < 1 || limit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}
			if cmd.Flags().Changed("contains") && strings.TrimSpace(contains) == "" {
				return fmt.Errorf("--contains must not be empty")
			}
			filter := digest.SessionSearchFilter{Since: start, Until: end, Contains: contains}
			if principal != nil {
				filter.Principal = client.SessionPrincipal{Kind: principal.Kind, ID: principal.ExternalID}
			}
			c, err := e.newClient()
			if err != nil {
				return err
			}
			if err := e.resolvePinnedProject(c); err != nil {
				return err
			}
			result, err := searchSessions(e, c, filter, surface, before, limit)
			if err != nil {
				return err
			}
			if render.IsJSON(e.mode(), e.out) {
				body, err := json.Marshal(result)
				if err != nil {
					return err
				}
				return e.renderJSON("session-search", body)
			}
			render.SessionSearch(e.out, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "principal-kind", "", "exact principal kind (requires --principal-id)")
	cmd.Flags().StringVar(&id, "principal-id", "", "exact principal ID (requires --principal-kind)")
	cmd.Flags().StringVar(&since, "since", "", "sessions created at/after RFC3339 or YYYY-MM-DD (UTC)")
	cmd.Flags().StringVar(&until, "until", "", "sessions created before RFC3339, or through YYYY-MM-DD (UTC)")
	cmd.Flags().StringVar(&contains, "contains", "", "case-insensitive literal in session title, opening preview or message prose")
	cmd.Flags().StringVar(&surface, "surface", "", "chat surface: embed|dashboard|all")
	cmd.Flags().StringVar(&before, "before", "", "resume an incomplete search with its next_before session ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "max matching sessions to return (1..100)")
	return cmd
}

func searchSessions(e *env, c *client.Client, filter digest.SessionSearchFilter, surface, before string, limit int) (*digest.SessionSearchResult, error) {
	result := &digest.SessionSearchResult{Project: e.scopeProject(), Tenant: e.scopeTenant(),
		Sessions: []json.RawMessage{}, Matches: []digest.SessionSearchMatch{}}
	params := client.SessionsParams{Project: result.Project, Tenant: result.Tenant, Surface: surface, Before: before, Limit: 100}
	seen := map[string]bool{}
	for result.Coverage.Pages < sessionSearchMaxPages {
		if seen[params.Before] {
			return nil, fmt.Errorf("session search: repeated cursor %q", params.Before)
		}
		seen[params.Before] = true
		page, raw, err := c.Sessions(e.ctx(), params)
		if err != nil {
			return nil, err
		}
		var rawPage struct {
			Sessions []json.RawMessage `json:"sessions"`
		}
		if err := json.Unmarshal(raw, &rawPage); err != nil {
			return nil, err
		}
		result.Coverage.Pages++
		for i, row := range page.Sessions {
			result.Coverage.Scanned++
			candidate, pastSince, err := filter.Candidate(row)
			if err != nil {
				return nil, err
			}
			if pastSince {
				result.Coverage.Complete, result.Coverage.Reason = true, "since"
				return result, nil
			}
			if !candidate {
				continue
			}
			match, found := digest.SessionPreviewMatch(row, filter.Contains)
			if !found {
				transcript, _, err := c.SessionTranscript(e.ctx(), result.Project, result.Tenant, row.SessionID)
				if err != nil {
					return nil, fmt.Errorf("session %s transcript: %w", row.SessionID, err)
				}
				result.Coverage.Transcripts++
				match, found = digest.SessionTranscriptMatch(row.SessionID, filter.Contains, transcript.Messages)
			}
			if found {
				result.Sessions = append(result.Sessions, rawPage.Sessions[i])
				result.Rows = append(result.Rows, row)
				result.Matches = append(result.Matches, match)
				if len(result.Sessions) == limit && (i+1 < len(page.Sessions) || page.NextBefore != "") {
					result.Coverage.Reason, result.Coverage.NextBefore = "result_limit", row.SessionID
					return result, nil
				}
			}
		}
		if page.NextBefore == "" {
			result.Coverage.Complete, result.Coverage.Reason = true, "exhausted"
			return result, nil
		}
		if len(page.Sessions) == 0 {
			return nil, fmt.Errorf("session search: empty page carries a continuation cursor")
		}
		params.Before = page.NextBefore
	}
	result.Coverage.Reason, result.Coverage.NextBefore = "page_limit", params.Before
	return result, nil
}
