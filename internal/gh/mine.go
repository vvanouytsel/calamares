package gh

import (
	"context"
	"fmt"
	"strings"
)

// The searches for your pull requests and issues read 30 at a time: each
// pull request carries the state of its checks, which is slow for GitHub to
// work out, and a page of 30 stays well inside its 10-second limit. A search
// with more reads further pages, up to maxMine.
const (
	minePage = 30
	maxMine  = 100
)

// mineSearch is one search of the things that concern you.
type mineSearch struct {
	alias, query string
}

// searchPage is a page of a search answer.
type searchPage[T any] struct {
	IssueCount int `json:"issueCount"`
	PageInfo   struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []T `json:"nodes"`
}

// mineQuery is the query named name that asks for the first page of each
// search in one request, with fragment (named fragName) for each node, and
// extra fields at the top.
func mineQuery(name string, searches []mineSearch, fragName, fragment, extra string) string {
	var b strings.Builder
	b.WriteString("query " + name + " {\n" + extra)
	for _, s := range searches {
		fmt.Fprintf(&b, "  %s: search(query: %q, type: ISSUE, first: %d) { issueCount pageInfo { hasNextPage endCursor } nodes { ...%s } }\n",
			s.alias, s.query, minePage, fragName)
	}
	return b.String() + "}\n" + fragment
}

// readRest reads the pages after first of search, until maxMine nodes are
// held or none are left. more says GitHub holds more than were read; a page
// that fails leaves what was read before it, counted as more.
func readRest[T any](ctx context.Context, c *Client, search mineSearch, fragName, fragment string, first searchPage[T]) (nodes []T, more bool) {
	nodes = first.Nodes
	query := `query SearchPage($q: String!, $after: String!, $n: Int!) {
  search(query: $q, type: ISSUE, first: $n, after: $after) { issueCount pageInfo { hasNextPage endCursor } nodes { ...` + fragName + ` } }
}
` + fragment
	page := first
	for page.PageInfo.HasNextPage && len(nodes) < maxMine {
		var data struct {
			Search searchPage[T] `json:"search"`
		}
		vars := map[string]any{"q": search.query, "after": page.PageInfo.EndCursor, "n": min(minePage, maxMine-len(nodes))}
		if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
			return nodes, true
		}
		page = data.Search
		nodes = append(nodes, page.Nodes...)
	}
	return nodes, page.PageInfo.HasNextPage || first.IssueCount > len(nodes)
}
