package gh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// issuePage is a search page of n issues numbered from first.
func issuePage(first, n, total int, next string) string {
	var nodes []string
	for i := range n {
		nodes = append(nodes, fmt.Sprintf(`{"number":%d,"title":"t","repository":{"nameWithOwner":"acme/webshop"},"lastComment":{"nodes":[]}}`, first+i))
	}
	return fmt.Sprintf(`{"issueCount":%d,"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}`, total, next != "", next, strings.Join(nodes, ","))
}

func TestASearchWithMoreThan30ReadsFurtherPagesUpTo100(t *testing.T) {
	var asked []string
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = append(asked, string(body))
		switch {
		case strings.Contains(string(body), `"after":"p2"`):
			fmt.Fprintf(w, `{"data":{"search":%s}}`, issuePage(31, 30, 140, "p3"))
		case strings.Contains(string(body), `"after":"p3"`):
			fmt.Fprintf(w, `{"data":{"search":%s}}`, issuePage(61, 30, 140, "p4"))
		case strings.Contains(string(body), `"after":"p4"`):
			fmt.Fprintf(w, `{"data":{"search":%s}}`, issuePage(91, 10, 140, "p5"))
		default:
			fmt.Fprintf(w, `{"data":{"viewer":{"login":"sam"},"assigned":%s,"created":%s,"mentioned":%s}}`,
				issuePage(1, 30, 140, "p2"), issuePage(500, 2, 2, ""), issuePage(600, 0, 0, ""))
		}
	})
	res, err := c.MyIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assigned) != 100 || !res.More[0] {
		t.Errorf("assigned: %d read, more %v; want 100 and more", len(res.Assigned), res.More[0])
	}
	if len(res.Created) != 2 || res.More[1] || res.More[2] {
		t.Errorf("created: %d read, more %v %v; want 2 and no more", len(res.Created), res.More[1], res.More[2])
	}
	if hits.Load() != 4 {
		t.Errorf("%d requests, want 4: the first page of all three, then three more of assigned", hits.Load())
	}
	if !strings.Contains(asked[len(asked)-1], `"n":10`) {
		t.Errorf("the last page should ask only for the 10 that make 100: %s", asked[len(asked)-1])
	}
}

func TestAPageThatFailsKeepsWhatWasReadAndSaysThereIsMore(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"after"`) {
			w.WriteHeader(http.StatusBadRequest) // not tried again, unlike a 502
			return
		}
		fmt.Fprintf(w, `{"data":{"review":{"issueCount":45,"pageInfo":{"hasNextPage":true,"endCursor":"p2"},"nodes":[{"number":1,"url":"u1","repository":{"nameWithOwner":"acme/webshop"}}]},
			"authored":{"nodes":[]},"involved":{"nodes":[]}}}`)
	})
	prs, more, err := c.MyPullRequests(context.Background())
	if err != nil || len(prs) != 1 || !more {
		t.Errorf("got %d pull requests, more %v, err %v; want the 1 read, more, no error", len(prs), more, err)
	}
}
