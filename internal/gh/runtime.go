package gh

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// JobTime is how long one job of a run took on a runner.
type JobTime struct {
	Name string        `json:"n"`
	Took time.Duration `json:"t"`
}

// RunsSince lists the finished runs of repo created since a day, newest
// first, reading at most pages pages of 100; more says there were more.
func (c *Client) RunsSince(ctx context.Context, repo string, since time.Time, pages int) (runs []Run, more bool, err error) {
	f := RunFilter{Status: "completed", Created: ">=" + since.UTC().Format("2006-01-02"), PerPage: 100}
	for page := 1; page <= pages; page++ {
		f.Page = page
		got, total, err := c.ListRuns(ctx, repo, f)
		if err != nil {
			return runs, false, err
		}
		runs = append(runs, got...)
		if len(got) < f.PerPage || len(runs) >= total {
			return runs, false, nil
		}
	}
	return runs, true, nil
}

// jobTimesBatch is how many runs one GraphQL request reads the jobs of.
const jobTimesBatch = 50

// RunJobTimes reads how long each job of many runs took, by their GraphQL
// ids: 50 runs to a request. A rerun's jobs count too, as they ran. Runs
// GitHub can't find are left out.
func (c *Client) RunJobTimes(ctx context.Context, nodeIDs []string) (map[string][]JobTime, error) {
	out := map[string][]JobTime{}
	for start := 0; start < len(nodeIDs); start += jobTimesBatch {
		batch := nodeIDs[start:min(start+jobTimesBatch, len(nodeIDs))]
		var params, fields []string
		vars := map[string]any{}
		for i, id := range batch {
			params = append(params, fmt.Sprintf("$id%d: ID!", i))
			fields = append(fields, fmt.Sprintf("r%d: node(id: $id%d) { ...jobTimes }", i, i))
			vars[fmt.Sprintf("id%d", i)] = id
		}
		query := "query RunJobTimes(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}\n" + jobTimesFragment
		var data map[string]*struct {
			CheckSuite *struct {
				CheckRuns struct {
					Nodes []struct {
						Name        string    `json:"name"`
						StartedAt   time.Time `json:"startedAt"`
						CompletedAt time.Time `json:"completedAt"`
					} `json:"nodes"`
				} `json:"checkRuns"`
			} `json:"checkSuite"`
		}
		if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
			return out, err
		}
		for i, id := range batch {
			r := data[fmt.Sprintf("r%d", i)]
			if r == nil || r.CheckSuite == nil {
				continue
			}
			list := []JobTime{}
			for _, cr := range r.CheckSuite.CheckRuns.Nodes {
				if cr.StartedAt.IsZero() || cr.CompletedAt.Before(cr.StartedAt) {
					continue
				}
				list = append(list, JobTime{Name: cr.Name, Took: cr.CompletedAt.Sub(cr.StartedAt)})
			}
			out[id] = list
		}
	}
	return out, nil
}

const jobTimesFragment = `fragment jobTimes on WorkflowRun {
  checkSuite { checkRuns(first: 100) { nodes { name startedAt completedAt } } }
}`
