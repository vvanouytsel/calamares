package gh

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// CodeOwnerRule is one line of a CODEOWNERS file: a path pattern and who owns it.
type CodeOwnerRule struct {
	Pattern string
	Owners  []string
}

// CodeOwners is the CODEOWNERS file of a repo. File is empty when the repo
// has none.
type CodeOwners struct {
	File  string
	Rules []CodeOwnerRule
}

// ParseCodeOwners reads the rules of a CODEOWNERS file. Comments and rules
// without an owner are left out.
func ParseCodeOwners(text string) []CodeOwnerRule {
	var rules []CodeOwnerRule
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if strings.HasPrefix(f, "#") {
				fields = fields[:i]
				break
			}
		}
		if len(fields) < 2 {
			continue
		}
		rules = append(rules, CodeOwnerRule{Pattern: fields[0], Owners: fields[1:]})
	}
	return rules
}

// Default is who owns the repo as a whole: the owners of the last "*" rule,
// or, when there is none, those named in most rules.
func (c CodeOwners) Default() []string {
	var catchAll []string
	count := map[string]int{}
	for _, r := range c.Rules {
		if r.Pattern == "*" {
			catchAll = r.Owners
		}
		for _, o := range r.Owners {
			count[o]++
		}
	}
	if catchAll != nil {
		return catchAll
	}
	var owners []string
	for o := range count {
		owners = append(owners, o)
	}
	sort.Slice(owners, func(i, j int) bool {
		if count[owners[i]] != count[owners[j]] {
			return count[owners[i]] > count[owners[j]]
		}
		return owners[i] < owners[j]
	})
	return owners
}

// codeOwnersFiles are the places GitHub looks for the file, in its order.
var codeOwnersFiles = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

const codeOwnersFragment = `fragment owners on Repository {
  nameWithOwner
  f0: object(expression: "HEAD:.github/CODEOWNERS") { ... on Blob { text } }
  f1: object(expression: "HEAD:CODEOWNERS") { ... on Blob { text } }
  f2: object(expression: "HEAD:docs/CODEOWNERS") { ... on Blob { text } }
}`

type codeOwnersRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
	F0            *struct{ Text string }
	F1            *struct{ Text string }
	F2            *struct{ Text string }
}

func (r codeOwnersRepo) owners() CodeOwners {
	for i, f := range []*struct{ Text string }{r.F0, r.F1, r.F2} {
		if f != nil {
			return CodeOwners{File: codeOwnersFiles[i], Rules: ParseCodeOwners(f.Text)}
		}
	}
	return CodeOwners{}
}

// codeOwnersBatch is how many repos one request reads.
const codeOwnersBatch = 50

// CodeOwners reads the CODEOWNERS file of each repo on its default branch,
// by repo name. Repos it may not read are left out.
func (c *Client) CodeOwners(ctx context.Context, repos []string) (map[string]CodeOwners, error) {
	out := map[string]CodeOwners{}
	for start := 0; start < len(repos); start += codeOwnersBatch {
		batch := repos[start:min(start+codeOwnersBatch, len(repos))]
		if err := c.codeOwnersOf(ctx, batch, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Client) codeOwnersOf(ctx context.Context, repos []string, into map[string]CodeOwners) error {
	var params, fields []string
	vars := map[string]any{}
	for i, repo := range repos {
		owner, name, _ := strings.Cut(repo, "/")
		params = append(params, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
		fields = append(fields, fmt.Sprintf("r%d: repository(owner: $o%d, name: $n%d) { ...owners }", i, i, i))
		vars[fmt.Sprintf("o%d", i)], vars[fmt.Sprintf("n%d", i)] = owner, name
	}
	query := "query CodeOwners(" + strings.Join(params, ", ") + ") {\n" + strings.Join(fields, "\n") + "\n}\n" + codeOwnersFragment
	var data map[string]*codeOwnersRepo
	if err := allowPartial(c.GraphQL(ctx, query, vars, &data)); err != nil {
		return err
	}
	for i := range repos {
		if r := data[fmt.Sprintf("r%d", i)]; r != nil {
			into[r.NameWithOwner] = r.owners()
		}
	}
	return nil
}
