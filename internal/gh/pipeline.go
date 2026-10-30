package gh

import (
	"context"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlannedJob is a job as a workflow file declares it, with the jobs it waits for.
type PlannedJob struct {
	ID    string
	Name  string // the job's name: line, or its ID when it has none
	Needs []string
	Uses  string // the reusable workflow the job calls, if any
	// Where the job is defined: the file, and its first and last line (from 1).
	File          *WorkflowFile
	Line, EndLine int
}

// WorkflowFile is a workflow file as it was at a ref, with its text.
type WorkflowFile struct {
	Repo, Path, Ref string
	Text            string
}

// ParsePipeline reads the jobs of a workflow file in file order, with what
// each one needs, so the shape of a run is known before its jobs start.
func ParsePipeline(src string) ([]PlannedJob, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	root := doc.Content[0]
	jobs := mapGet(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, nil
	}
	lines := strings.Split(src, "\n")
	jobsEnd := len(lines)
	for i := 0; i+2 < len(root.Content); i += 2 {
		if root.Content[i].Value == "jobs" {
			jobsEnd = root.Content[i+2].Line - 1
		}
	}
	var out []PlannedJob
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		job := PlannedJob{ID: jobs.Content[i].Value, Line: jobs.Content[i].Line, EndLine: jobsEnd}
		if i+2 < len(jobs.Content) {
			job.EndLine = jobs.Content[i+2].Line - 1
		}
		job.EndLine = lastContentLine(lines, job.Line, job.EndLine)
		job.Name = job.ID
		if spec := jobs.Content[i+1]; spec.Kind == yaml.MappingNode {
			if n := mapGet(spec, "name"); n != nil && n.Value != "" {
				job.Name = n.Value
			}
			if n := mapGet(spec, "uses"); n != nil {
				job.Uses = n.Value
			}
			if n := mapGet(spec, "needs"); n != nil {
				switch n.Kind {
				case yaml.ScalarNode:
					job.Needs = []string{n.Value}
				case yaml.SequenceNode:
					for _, x := range n.Content {
						job.Needs = append(job.Needs, x.Value)
					}
				}
			}
		}
		out = append(out, job)
	}
	return out, nil
}

// lastContentLine moves end up past blank and comment lines, which belong
// to whatever follows rather than to the job before them.
func lastContentLine(lines []string, start, end int) int {
	for end > start && end <= len(lines) {
		if t := strings.TrimSpace(lines[end-1]); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return end
}

// maxReusableDepth is how many levels of called workflows are followed.
const maxReusableDepth = 3

// ResolvePipeline reads the workflow file of a run at sha and the reusable
// workflows it calls, and returns every job that can run, with its
// dependencies and where it is defined. A job that calls a workflow is
// replaced by that workflow's jobs, named "caller / job" as GitHub names them.
func (c *Client) ResolvePipeline(ctx context.Context, repo, sha, path string) ([]PlannedJob, error) {
	file, jobs, err := c.readWorkflow(ctx, repo, path, sha)
	if err != nil {
		return nil, err
	}
	return c.expandCalls(ctx, file, jobs, "", "", 0), nil
}

// readWorkflow reads a workflow file at ref and its jobs, each pointing back to it.
func (c *Client) readWorkflow(ctx context.Context, repo, path, ref string) (*WorkflowFile, []PlannedJob, error) {
	src, err := c.FileContent(ctx, repo, path, ref)
	if err != nil {
		return nil, nil, err
	}
	jobs, err := ParsePipeline(src)
	if err != nil {
		return nil, nil, err
	}
	file := &WorkflowFile{Repo: repo, Path: path, Ref: ref, Text: src}
	for i := range jobs {
		jobs[i].File = file
	}
	return file, jobs, nil
}

func (c *Client) expandCalls(ctx context.Context, file *WorkflowFile, jobs []PlannedJob, idPrefix, namePrefix string, depth int) []PlannedJob {
	var out []PlannedJob
	leaves := map[string][]string{}
	for _, j := range jobs {
		full := j
		full.ID, full.Name, full.Needs = idPrefix+j.ID, namePrefix+j.Name, nil
		for _, n := range j.Needs {
			full.Needs = append(full.Needs, idPrefix+n)
		}
		called, children := c.calledJobs(ctx, file, j.Uses, depth)
		if len(children) == 0 {
			out = append(out, full)
			continue
		}
		nested := c.expandCalls(ctx, called, children, full.ID+"/", full.Name+" / ", depth+1)
		needed := map[string]bool{}
		for _, n := range nested {
			for _, k := range n.Needs {
				needed[k] = true
			}
		}
		for i := range nested {
			if len(nested[i].Needs) == 0 {
				nested[i].Needs = full.Needs
			}
			if !needed[nested[i].ID] {
				leaves[full.ID] = append(leaves[full.ID], nested[i].ID)
			}
		}
		out = append(out, nested...)
	}
	for i := range out {
		var needs []string
		for _, n := range out[i].Needs {
			if l, ok := leaves[n]; ok {
				needs = append(needs, l...)
			} else {
				needs = append(needs, n)
			}
		}
		out[i].Needs = needs
	}
	return out
}

// calledJobs reads the reusable workflow named by a job's uses: value, or
// returns nothing when it is not one or cannot be read. A local path is
// relative to the repo and ref of the file that calls it.
func (c *Client) calledJobs(ctx context.Context, from *WorkflowFile, uses string, depth int) (*WorkflowFile, []PlannedJob) {
	if uses == "" || depth >= maxReusableDepth {
		return nil, nil
	}
	repo, ref, path := from.Repo, from.Ref, ""
	if local, ok := strings.CutPrefix(uses, "./"); ok {
		path = local
	} else {
		spec, at, _ := strings.Cut(uses, "@")
		parts := strings.SplitN(spec, "/", 3)
		if len(parts) != 3 {
			return nil, nil
		}
		repo, path, ref = parts[0]+"/"+parts[1], parts[2], at
	}
	file, jobs, err := c.readWorkflow(ctx, repo, path, ref)
	if err != nil {
		return nil, nil
	}
	return file, jobs
}
