package gh

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestMyIssuesSortsOutHowEachConcernsYou(t *testing.T) {
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"sam"},
		 "assigned":{"nodes":[
		   {"number":9,"title":"Prices differ","repository":{"nameWithOwner":"acme/webshop"},"assignees":{"nodes":[{"login":"sam"}]},
		    "lastComment":{"nodes":[{"author":{"login":"robin"},"body":"@sam can you take a look?","createdAt":"2026-10-14T08:00:00Z"}]}},
		   {"number":3,"title":"Docs","repository":{"nameWithOwner":"acme/handbook"},"assignees":{"nodes":[{"login":"sam"}]},"lastComment":{"nodes":[]}}]},
		 "created":{"nodes":[{"number":14,"title":"Dark mode","repository":{"nameWithOwner":"acme/webshop"},"lastComment":{"nodes":[]},
		   "closedByPullRequestsReferences":{"nodes":[{"number":61,"title":"fix","state":"OPEN","repository":{"nameWithOwner":"acme/webshop"},"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}}]}},
		   {"number":3,"title":"Docs","repository":{"nameWithOwner":"acme/handbook"},"lastComment":{"nodes":[]}}]},
		 "mentioned":{"nodes":[{"number":22,"title":"Buttons","repository":{"nameWithOwner":"acme/design"},"lastComment":{"nodes":[]}}]}}}`))
	})
	res, err := c.MyIssues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests, want 1", hits.Load())
	}
	why := map[int]string{}
	for _, l := range [][]Issue{res.Assigned, res.Created, res.Mentioned} {
		for _, i := range l {
			why[i.Number] = i.Why
		}
	}
	for n, want := range map[int]string{9: "asked", 3: "assigned", 14: "author", 22: "mention"} {
		if why[n] != want {
			t.Errorf("#%d is %q, want %q", n, why[n], want)
		}
	}
	if got := res.Created[0].Linked; len(got) != 1 || got[0].Checks != "FAILURE" || got[0].Number != 61 {
		t.Errorf("linked %+v", got)
	}
	if !res.Assigned[0].WaitsForYou("sam") || res.Assigned[1].WaitsForYou("sam") {
		t.Errorf("waits for you wrong")
	}
}

func TestIssueTemplatesFillFromFrontMatterAndForms(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/contents/.github/ISSUE_TEMPLATE"):
			_, _ = w.Write([]byte(`[{"name":"bug.md","type":"file"},{"name":"idea.yml","type":"file"},{"name":"config.yml","type":"file"}]`))
		case strings.HasSuffix(r.URL.Path, "bug.md"):
			_, _ = w.Write([]byte(`{"encoding":"base64","content":"` + b64("---\nname: Bug report\nabout: Something broke\ntitle: \"[bug] \"\nlabels: bug, triage\n---\n\n**What happened?**\n") + `"}`))
		case strings.HasSuffix(r.URL.Path, "idea.yml"):
			_, _ = w.Write([]byte(`{"encoding":"base64","content":"` + b64("name: Idea\ndescription: Suggest one\nbody:\n  - type: textarea\n    attributes:\n      label: The idea\n  - type: input\n    attributes:\n      label: Why now\n") + `"}`))
		}
	})
	ts, err := c.IssueTemplates(context.Background(), "a/b")
	if err != nil || len(ts) != 2 {
		t.Fatalf("%v %+v", err, ts)
	}
	if ts[0].Name != "Bug report" || ts[0].Title != "[bug] " || len(ts[0].Labels) != 2 || !strings.Contains(ts[0].Body, "What happened") {
		t.Errorf("markdown template %+v", ts[0])
	}
	if ts[1].Name != "Idea" || !strings.Contains(ts[1].Body, "### The idea") || !strings.Contains(ts[1].Body, "### Why now") {
		t.Errorf("form template %+v", ts[1])
	}
}

func TestACloseSendsTheReason(t *testing.T) {
	var got string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 200)
		n, _ := r.Body.Read(b)
		got = r.Method + " " + r.URL.Path + " " + string(b[:n])
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.CloseIssue(context.Background(), "a/b", 9, "not_planned"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "PATCH /repos/a/b/issues/9") || !strings.Contains(got, `"state_reason":"not_planned"`) {
		t.Errorf("request %q", got)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestNextVersionsAfterATag(t *testing.T) {
	p, m, M := NextVersions("v2.15.3")
	if p != "v2.15.4" || m != "v2.16.0" || M != "v3.0.0" {
		t.Errorf("%s %s %s", p, m, M)
	}
	if p, _, _ := NextVersions("nightly"); p != "" {
		t.Errorf("a tag that is not a version should propose nothing")
	}
}

func TestBranchesCompareFromTheBranchesPointOfView(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		// compare(headRef: default) from the branch: the default is 12 ahead, the branch 3 behind it.
		_, _ = w.Write([]byte(`{"data":{"repository":{"defaultBranchRef":{"name":"main"},"refs":{"nodes":[
		 {"name":"main","compare":{"aheadBy":0,"behindBy":0},"associatedPullRequests":{"nodes":[]},"target":{"committedDate":"2026-10-14T00:00:00Z"}},
		 {"name":"feat","branchProtectionRule":null,"compare":{"aheadBy":12,"behindBy":3},"associatedPullRequests":{"nodes":[{"number":61,"state":"MERGED","isDraft":false}]},
		  "target":{"committedDate":"2026-10-10T00:00:00Z","messageHeadline":"feat: x","author":{"user":{"login":"robin"}},"statusCheckRollup":{"state":"SUCCESS"}}}]}}}}`))
	})
	bs, err := c.Branches(context.Background(), "a/b", "main")
	if err != nil || len(bs) != 2 || !bs[0].Default {
		t.Fatalf("%v %+v", err, bs)
	}
	b := bs[1]
	if b.Ahead != 3 || b.Behind != 12 || b.PR == nil || b.PR.State != "MERGED" || b.Author != "robin" || b.Checks != "SUCCESS" {
		t.Errorf("%+v", b)
	}
}
