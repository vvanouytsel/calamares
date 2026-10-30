package gh

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Use is a reusable workflow or an action that a file refers to with uses:.
type Use struct {
	Repo    string // the repo whose file refers to it
	File    string // that file, such as .github/workflows/build.yml or action.yml
	Target  string // owner/repo, owner/repo/path, or owner/repo/.github/workflows/file.yml
	Version string // a tag, a branch or a commit
	// Job is the name a job that calls a reusable workflow shows in a run
	// (its name: or, without one, its id), so the jobs of a run can be told
	// apart by the workflow they ran in; "" for actions.
	Job string `json:",omitempty"`
}

// Workflow says the target is a reusable workflow rather than an action.
func (u Use) Workflow() bool { return strings.Contains(u.Target, "/.github/workflows/") }

// TargetRepo is the owner/repo the target lives in.
func (u Use) TargetRepo() string {
	parts := strings.SplitN(u.Target, "/", 3)
	if len(parts) < 2 {
		return u.Target
	}
	return parts[0] + "/" + parts[1]
}

// TargetFile is the target's path in its repo: the workflow file, the folder
// of an action, or "" for an action at the root.
func (u Use) TargetFile() string {
	parts := strings.SplitN(u.Target, "/", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

var usesLine = regexp.MustCompile(`(?m)^\s*(?:-\s*)?uses:\s*["']?([^\s"'#]+)`)

// ParseUses lists what a workflow or action file uses; local paths and
// container images are left out, as they are no one else's.
func ParseUses(repo, file, text string) []Use {
	var out []Use
	for _, m := range usesLine.FindAllStringSubmatch(text, -1) {
		ref := m[1]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") || !strings.Contains(ref, "/") {
			continue
		}
		target, version, _ := strings.Cut(ref, "@")
		out = append(out, Use{Repo: repo, File: file, Target: target, Version: version})
	}
	if jobs := callerJobs(text); len(jobs) > 0 {
		for i := range out {
			if out[i].Workflow() {
				out[i].Job = jobs[out[i].Target+"@"+out[i].Version]
			}
		}
	}
	return out
}

// callerJobs maps each reusable workflow a workflow file calls, as
// target@version, to the name of the job that calls it.
func callerJobs(text string) map[string]string {
	var doc yaml.Node
	if yaml.Unmarshal([]byte(text), &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	jobs := mapGet(doc.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		id, job := jobs.Content[i].Value, jobs.Content[i+1]
		if job.Kind != yaml.MappingNode {
			continue
		}
		uses := mapGet(job, "uses")
		if uses == nil || uses.Kind != yaml.ScalarNode {
			continue
		}
		name := id
		if n := mapGet(job, "name"); n != nil && n.Kind == yaml.ScalarNode && n.Value != "" && !strings.Contains(n.Value, "${{") {
			name = n.Value
		}
		out[uses.Value] = name
	}
	return out
}

// Schedule is a workflow file with a schedule: trigger, and its cron lines.
type Schedule struct {
	Repo  string
	File  string
	Crons []string
}

var cronLine = regexp.MustCompile(`(?m)^\s*(?:-\s*)?cron:\s*["']?([^"'#\n]*[^"'#\s])`)

// ParseCrons lists the cron expressions of a workflow file's schedule trigger.
func ParseCrons(text string) []string {
	var out []string
	for _, m := range cronLine.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// Scan is what the workflow files of some repos use, and which of them run
// on a schedule.
type Scan struct {
	Uses      []Use
	Schedules []Schedule
}

func (s *Scan) add(more Scan) {
	s.Uses = append(s.Uses, more.Uses...)
	s.Schedules = append(s.Schedules, more.Schedules...)
}

// usesBatch is how many repos one request reads. GitHub counts a request by
// the nodes it may return, so a batch costs one point however large it is,
// but gives up on a query that takes over ten seconds.
const usesBatch = 25

const usesFragment = `fragment files on Repository {
  nameWithOwner
  workflows: object(expression: "HEAD:.github/workflows") { ... on Tree { entries { path object { ... on Blob { text } } } } }
  action: object(expression: "HEAD:action.yml") { ... on Blob { text } }
  actionYaml: object(expression: "HEAD:action.yaml") { ... on Blob { text } }
}`

type usesRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
	Workflows     *struct {
		Entries []struct {
			Path   string `json:"path"`
			Object *struct {
				Text string `json:"text"`
			} `json:"object"`
		} `json:"entries"`
	} `json:"workflows"`
	Action     *struct{ Text string } `json:"action"`
	ActionYaml *struct{ Text string } `json:"actionYaml"`
}

func (r usesRepo) scan() Scan {
	var out Scan
	if r.Workflows != nil {
		for _, e := range r.Workflows.Entries {
			if e.Object == nil {
				continue
			}
			out.Uses = append(out.Uses, ParseUses(r.NameWithOwner, e.Path, e.Object.Text)...)
			if crons := ParseCrons(e.Object.Text); len(crons) > 0 {
				out.Schedules = append(out.Schedules, Schedule{Repo: r.NameWithOwner, File: e.Path, Crons: crons})
			}
		}
	}
	if r.Action != nil {
		out.Uses = append(out.Uses, ParseUses(r.NameWithOwner, "action.yml", r.Action.Text)...)
	}
	if r.ActionYaml != nil {
		out.Uses = append(out.Uses, ParseUses(r.NameWithOwner, "action.yaml", r.ActionYaml.Text)...)
	}
	return out
}

// Uses reads the workflow files and the action at the root of each repo on
// its default branch, and lists what they use. Repos it may not read are
// left out.
func (c *Client) Uses(ctx context.Context, repos []string) ([]Use, error) {
	s, err := c.ScanWorkflows(ctx, repos)
	return s.Uses, err
}

// ScanWorkflows reads the same files as Uses, and also says which workflows
// run on a schedule.
func (c *Client) ScanWorkflows(ctx context.Context, repos []string) (Scan, error) {
	var out Scan
	for start := 0; start < len(repos); start += usesBatch {
		batch := repos[start:min(start+usesBatch, len(repos))]
		s, err := c.usesSplitting(ctx, batch)
		if err != nil {
			return Scan{}, err
		}
		out.add(s)
	}
	return out, nil
}

// usesSplitting reads repos, in two halves when GitHub fails on them all
// at once, as it does when the files take too long to gather.
func (c *Client) usesSplitting(ctx context.Context, repos []string) (Scan, error) {
	once := ctx
	if len(repos) >= 2 {
		once = withoutRetry(ctx) // split at once instead of asking for it all again
	}
	s, err := c.usesOf(once, repos)
	if err == nil || len(repos) < 2 || !IsServerError(err) {
		return s, err
	}
	half := len(repos) / 2
	first, err := c.usesSplitting(ctx, repos[:half])
	if err != nil {
		return Scan{}, err
	}
	second, err := c.usesSplitting(ctx, repos[half:])
	first.add(second)
	return first, err
}

func (c *Client) usesOf(ctx context.Context, repos []string) (Scan, error) {
	var params, fields []string
	vars := map[string]any{}
	for i, repo := range repos {
		owner, name, _ := strings.Cut(repo, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		fields = append(fields, fmt.Sprintf("r%d: repository(owner: $o%d, name: $n%d) { ...files }", i, i, i))
		vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
	}
	query := "query Uses(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}\n" + usesFragment
	var data map[string]*usesRepo
	if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
		return Scan{}, err
	}
	var out Scan
	for i := range repos {
		if r := data[fmt.Sprintf("r%d", i)]; r != nil {
			out.add(r.scan())
		}
	}
	return out, nil
}
