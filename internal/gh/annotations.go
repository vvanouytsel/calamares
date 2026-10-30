package gh

import (
	"context"
	"fmt"
	"strings"
)

// JobAnnotation is an annotation of one job of a run.
type JobAnnotation struct {
	Job     string
	Level   string // FAILURE, WARNING or NOTICE
	Message string
	Path    string
}

// annotationsBatch is how many runs one GraphQL request reads the
// annotations of; a run's jobs and their annotations are capped below so a
// request stays within GitHub's node limit.
const annotationsBatch = 25

// RunAnnotations reads the annotations of the jobs of many runs, by their
// GraphQL ids: 25 runs to a request instead of one request per job. Runs
// GitHub can't find are left out.
func (c *Client) RunAnnotations(ctx context.Context, nodeIDs []string) (map[string][]JobAnnotation, error) {
	out := map[string][]JobAnnotation{}
	for start := 0; start < len(nodeIDs); start += annotationsBatch {
		batch := nodeIDs[start:min(start+annotationsBatch, len(nodeIDs))]
		var params, fields []string
		vars := map[string]any{}
		for i, id := range batch {
			params = append(params, fmt.Sprintf("$id%d: ID!", i))
			fields = append(fields, fmt.Sprintf("r%d: node(id: $id%d) { ...annotations }", i, i))
			vars[fmt.Sprintf("id%d", i)] = id
		}
		query := "query RunAnnotations(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}\n" + annotationsFragment
		var data map[string]*struct {
			CheckSuite *struct {
				CheckRuns struct {
					Nodes []struct {
						Name        string `json:"name"`
						Annotations struct {
							Nodes []struct {
								Level   string `json:"annotationLevel"`
								Message string `json:"message"`
								Path    string `json:"path"`
							} `json:"nodes"`
						} `json:"annotations"`
					} `json:"nodes"`
				} `json:"checkRuns"`
			} `json:"checkSuite"`
		}
		if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
			return nil, err
		}
		for i, id := range batch {
			r := data[fmt.Sprintf("r%d", i)]
			if r == nil || r.CheckSuite == nil {
				continue
			}
			list := []JobAnnotation{}
			for _, cr := range r.CheckSuite.CheckRuns.Nodes {
				for _, an := range cr.Annotations.Nodes {
					list = append(list, JobAnnotation{Job: cr.Name, Level: an.Level, Message: an.Message, Path: an.Path})
				}
			}
			out[id] = list
		}
	}
	return out, nil
}

const annotationsFragment = `fragment annotations on WorkflowRun {
  checkSuite { checkRuns(first: 30) { nodes { name annotations(first: 10) { nodes { annotationLevel message path } } } } }
}`
