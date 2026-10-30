package gh

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubStatusReadsPartsIncidentsAndMaintenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/summary.json":
			w.Write([]byte(`{"components":[{"name":"Actions","status":"degraded_performance"},{"name":"Visit www.githubstatus.com for more information","status":"operational"}],
"scheduled_maintenances":[{"id":"m1","name":"Packages maintenance","status":"scheduled","scheduled_for":"2026-10-24T02:00:00Z","scheduled_until":"2026-10-24T04:00:00Z","components":[{"name":"Packages"}]}]}`))
		case "/api/v2/incidents.json":
			w.Write([]byte(`{"incidents":[{"id":"i1","name":"Actions jobs delayed","status":"investigating","impact":"minor","shortlink":"https://stspg.io/x","created_at":"2026-10-18T14:20:00Z","resolved_at":null,
"incident_updates":[{"status":"investigating","body":"We are investigating.","created_at":"2026-10-18T14:20:00Z"}],"components":[{"name":"Actions"}]},
{"id":"i0","name":"Webhooks late","status":"resolved","impact":"minor","created_at":"2026-10-16T10:00:00Z","resolved_at":"2026-10-16T11:12:00Z","components":[{"name":"Webhooks"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewWithToken("github.com", "x", "test")
	if _, ok, _ := c.GitHubStatus(context.Background()); ok {
		t.Fatal("a client without a status page should say so")
	}
	c.StatusURL = srv.URL
	s, ok, err := c.GitHubStatus(context.Background())
	if !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if len(s.Components) != 1 || s.Components[0].Status != "degraded_performance" {
		t.Errorf("components = %+v, want Actions only", s.Components)
	}
	if len(s.Incidents) != 2 || !s.Incidents[0].Open() || s.Incidents[1].Open() || s.Incidents[0].Components[0] != "Actions" {
		t.Errorf("incidents = %+v", s.Incidents)
	}
	if len(s.Maintenances) != 1 || s.Maintenances[0].Components[0] != "Packages" {
		t.Errorf("maintenances = %+v", s.Maintenances)
	}
}
