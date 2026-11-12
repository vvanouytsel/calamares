package gh

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPersonActivityReadsWhatSomeoneDidInOneQueryAndOneSearch(t *testing.T) {
	var vars map[string]any
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/commits") {
			if q := r.URL.Query().Get("q"); q != "author:sam author-date:>=2026-10-28" {
				t.Errorf("commit search %q", q)
			}
			_, _ = w.Write([]byte(`{"items":[{"sha":"abc1234def","html_url":"u","commit":{"message":"fix: round prices\n\nlonger","author":{"date":"2026-11-10T09:00:00Z"}},"repository":{"full_name":"acme/webshop"}}]}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct{ Variables map[string]any }
		_ = json.Unmarshal(body, &req)
		vars = req.Variables
		_, _ = w.Write([]byte(`{"data":{"user":{"login":"sam","name":"Sam Kimura"},
			"opened":{"nodes":[{"number":57,"title":"feat: delivery estimates","state":"OPEN","createdAt":"2026-11-10T08:00:00Z","author":{"login":"sam"},"repository":{"nameWithOwner":"acme/webshop"}}]},
			"reviewed":{"nodes":[{"number":61,"title":"fix: refunds","state":"OPEN","author":{"login":"robin"},"repository":{"nameWithOwner":"acme/payments"},"reviews":{"nodes":[{"state":"APPROVED","submittedAt":"2026-11-10T07:00:00Z"}]}}]},
			"issues":{"nodes":[{"number":33,"title":"Refund webhook times out","state":"OPEN","createdAt":"2026-11-09T07:00:00Z","repository":{"nameWithOwner":"acme/payments"}}]},
			"forYou":{"nodes":[{"number":57,"title":"feat: delivery estimates","state":"OPEN","repository":{"nameWithOwner":"acme/webshop"}}]},
			"forThem":{"nodes":[]}}}`))
	})
	p, err := c.PersonActivity(t.Context(), "sam", time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want one query and one commit search", hits.Load())
	}
	if vars["opened"] != "is:pr author:sam updated:>=2026-10-28 sort:updated-desc" || vars["forThem"] != "is:pr is:open author:@me review-requested:sam sort:updated-desc" {
		t.Errorf("searches: %v", vars)
	}
	if p.Name != "Sam Kimura" || len(p.Opened) != 1 || len(p.ForYou) != 1 || len(p.Issues) != 1 {
		t.Errorf("person %+v", p)
	}
	if r := p.Reviewed; len(r) != 1 || r[0].Review != "APPROVED" || r[0].Author != "robin" || r[0].Reviewed.IsZero() {
		t.Errorf("reviews %+v", r)
	}
	if cs := p.Commits; len(cs) != 1 || cs[0].Message != "fix: round prices" || cs[0].Repo != "acme/webshop" {
		t.Errorf("commits %+v", cs)
	}
}

func TestABotIsSearchedAsAnApp(t *testing.T) {
	if searchName("dependabot[bot]") != "app/dependabot" || searchName("sam") != "sam" {
		t.Errorf("searchName: %q %q", searchName("dependabot[bot]"), searchName("sam"))
	}
	for login, want := range map[string]bool{"sam": true, "sam-kimura": true, "dependabot[bot]": true, "sam_acme": true, "sam-kimura_acme": true,
		"": false, "sam kimura": false, "-sam": false, "sam/x": false, "sam_": false, "_acme": false, "sam_acme_x": false} {
		if ValidLogin(login) != want {
			t.Errorf("ValidLogin(%q) = %v", login, !want)
		}
	}
}

func TestOrgMembersReadsEveryOrganizationPageByPage(t *testing.T) {
	var asked []map[string]any
	c, hits := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct{ Variables map[string]any }
		_ = json.Unmarshal(body, &req)
		asked = append(asked, req.Variables)
		if req.Variables["org"] == "acme" {
			_, _ = w.Write([]byte(`{"data":{"organization":{"membersWithRole":{"pageInfo":{"hasNextPage":false},
				"nodes":[{"login":"robin","name":""}]}}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"organizations":{"nodes":[
			{"login":"acme","membersWithRole":{"pageInfo":{"hasNextPage":true,"endCursor":"c1"},"nodes":[{"login":"sam_acme","name":"Sam Kimura"}]}},
			{"login":"acme-labs","membersWithRole":{"pageInfo":{"hasNextPage":false},"nodes":[{"login":"sam_acme","name":"Sam Kimura"},{"login":"alex","name":"Alex Roos"}]}}]}}}}`))
	})
	got, err := c.OrgMembers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || asked[1]["after"] != "c1" {
		t.Errorf("%d requests %v, want the organizations and then the next page of acme", hits.Load(), asked)
	}
	want := []Member{{Login: "sam_acme", Name: "Sam Kimura", Org: "acme"}, {Login: "robin", Org: "acme"}, {Login: "alex", Name: "Alex Roos", Org: "acme-labs"}}
	if len(got) != len(want) {
		t.Fatalf("members %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("member %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
