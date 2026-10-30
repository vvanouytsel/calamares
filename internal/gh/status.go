package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// githubStatus is GitHub's public status page. It needs no token and costs
// nothing of the API budget; a GitHub Enterprise Server has none.
const githubStatus = "https://www.githubstatus.com"

// StatusComponent is a part of GitHub and how it is doing: operational,
// degraded_performance, partial_outage, major_outage or under_maintenance.
type StatusComponent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// IncidentUpdate is one message GitHub posted about an incident.
type IncidentUpdate struct {
	Status string    `json:"status"`
	Body   string    `json:"body"`
	At     time.Time `json:"created_at"`
}

// Incident is something GitHub reported going wrong. Status is
// investigating, identified, monitoring, resolved or postmortem; Impact is
// none, minor, major or critical.
type Incident struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Status     string           `json:"status"`
	Impact     string           `json:"impact"`
	URL        string           `json:"shortlink"`
	CreatedAt  time.Time        `json:"created_at"`
	ResolvedAt *time.Time       `json:"resolved_at"`
	Updates    []IncidentUpdate `json:"incident_updates"`
	Components []string         `json:"-"`
}

// Open says GitHub hasn't resolved it yet.
func (i Incident) Open() bool {
	return i.ResolvedAt == nil && i.Status != "resolved" && i.Status != "postmortem"
}

// Ended is when it was resolved, or the zero time while it is open.
func (i Incident) Ended() time.Time {
	if i.ResolvedAt == nil {
		return time.Time{}
	}
	return *i.ResolvedAt
}

// Maintenance is planned work on GitHub.
type Maintenance struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"` // scheduled, in_progress, verifying or completed
	URL        string    `json:"shortlink"`
	From       time.Time `json:"scheduled_for"`
	Until      time.Time `json:"scheduled_until"`
	Components []string  `json:"-"`
}

// Status is what GitHub's status page says: how each part is doing, the
// incidents of late (open ones first) and the maintenance coming up.
type Status struct {
	Components   []StatusComponent
	Incidents    []Incident
	Maintenances []Maintenance
}

type namedComponents []struct {
	Name string `json:"name"`
}

func (n namedComponents) names() []string {
	var out []string
	for _, c := range n {
		out = append(out, c.Name)
	}
	return out
}

// GitHubStatus reads GitHub's status page: two requests, at most every few
// minutes. A client for a GitHub Enterprise Server has no status page and
// says so with ok false.
func (c *Client) GitHubStatus(ctx context.Context) (s Status, ok bool, err error) {
	if c.StatusURL == "" {
		return Status{}, false, nil
	}
	var summary struct {
		Components   []StatusComponent `json:"components"`
		Maintenances []struct {
			Maintenance
			Components namedComponents `json:"components"`
		} `json:"scheduled_maintenances"`
	}
	if err := c.statusGet(ctx, "/api/v2/summary.json", &summary); err != nil {
		return Status{}, true, err
	}
	var incidents struct {
		Incidents []struct {
			Incident
			Components namedComponents `json:"components"`
		} `json:"incidents"`
	}
	if err := c.statusGet(ctx, "/api/v2/incidents.json", &incidents); err != nil {
		return Status{}, true, err
	}
	for _, comp := range summary.Components {
		if !strings.HasPrefix(comp.Name, "Visit ") {
			s.Components = append(s.Components, comp)
		}
	}
	for _, m := range summary.Maintenances {
		m.Maintenance.Components = m.Components.names()
		s.Maintenances = append(s.Maintenances, m.Maintenance)
	}
	for _, in := range incidents.Incidents {
		in.Incident.Components = in.Components.names()
		s.Incidents = append(s.Incidents, in.Incident)
	}
	return s, true, nil
}

func (c *Client) statusGet(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.StatusURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the status page answered %s", resp.Status)
	}
	return json.Unmarshal(body, v)
}
