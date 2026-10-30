package ui

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// Shared workflows and actions are what your repos use of each other's. Who
// uses which version comes from the workflow files on each repo's default
// branch (see gh.Uses); until those are read, from the runs we hold, as
// GitHub names on every run the workflows it called. How a shared workflow is
// doing always comes from those runs.

// sharedItem is a reusable workflow or an action, who uses it at which
// version, and the runs we hold that called it.
type sharedItem struct {
	repo   string
	path   string // the workflow file, or the folder of an action ("" at the root)
	action bool
	// versions are those in use, the newest tag first, then branches and commits.
	versions []*sharedVersion
	calls    []sharedCall // newest first
	inside   []string     // the shared workflows and actions that use it, by key
	nested   []string     // the shared workflows this one calls, by key
	pass     int          // the share of finished calls that passed, or -1
	usual    time.Duration
}

// sharedVersion is one version of a shared item and who uses it.
type sharedVersion struct {
	version string
	users   []sharedUser
	calls   []sharedCall // the runs that called the item at this version, newest first
	behind  bool         // an older tag than another repo uses
	ahead   string       // the newest tag it is behind
}

// sharedUser is a repo and the file in it that uses a shared item.
type sharedUser struct{ repo, file string }

// sharedCall is a run that called a shared workflow at version.
type sharedCall struct {
	*repoRun
	version string
}

// sharedKey names a shared item; GitHub ignores case in owners and repos,
// and files spell them either way.
func sharedKey(repo, p string) string {
	if p == "" {
		return strings.ToLower(repo)
	}
	return strings.ToLower(repo + "/" + p)
}

func (s *sharedItem) key() string { return sharedKey(s.repo, s.path) }

func (s *sharedItem) name() string {
	if !s.action {
		return path.Base(s.path)
	}
	_, name, _ := strings.Cut(s.repo, "/")
	if s.path != "" {
		name += "/" + s.path
	}
	return name
}

// repoCount is how many repos use it, at any version.
func (s *sharedItem) repoCount() int {
	seen := map[string]bool{}
	for _, v := range s.versions {
		for _, u := range v.users {
			seen[u.repo] = true
		}
	}
	return len(seen)
}

func (s *sharedItem) version(version string) *sharedVersion {
	for _, v := range s.versions {
		if v.version == version {
			return v
		}
	}
	return nil
}

// versionOf is the version repo uses.
func (s *sharedItem) versionOf(repo string) (*sharedVersion, bool) {
	for _, v := range s.versions {
		for _, u := range v.users {
			if strings.EqualFold(u.repo, repo) {
				return v, true
			}
		}
	}
	return nil, false
}

func (s *sharedItem) usedBy(repo string) bool {
	_, ok := s.versionOf(repo)
	return ok
}

// lastCall is the newest run of repo that called it, or of anyone for "".
func lastCall(calls []sharedCall, repo string) (sharedCall, bool) {
	for _, c := range calls {
		if repo == "" || strings.EqualFold(c.repo, repo) {
			return c, true
		}
	}
	return sharedCall{}, false
}

// callKinds are the results of calls, oldest first.
func callKinds(calls []sharedCall) []Kind {
	out := make([]Kind, 0, len(calls))
	for i := len(calls) - 1; i >= 0; i-- {
		out = append(out, StatusKind(calls[i].run.Status, calls[i].run.Conclusion))
	}
	return out
}

func callMedian(calls []sharedCall) time.Duration {
	var ds []time.Duration
	now := time.Now()
	for _, c := range calls {
		if c.run.Status == "completed" && c.run.Conclusion == "success" {
			ds = append(ds, c.run.Duration(now))
		}
	}
	return median(ds)
}

func (s *sharedItem) addUser(version string, u sharedUser) {
	v := s.version(version)
	if v == nil {
		v = &sharedVersion{version: version}
		s.versions = append(s.versions, v)
	}
	for _, have := range v.users {
		if strings.EqualFold(have.repo, u.repo) {
			return
		}
	}
	v.users = append(v.users, u)
}

// usedFrom is the key of the shared item a use is written in, when its file
// is a workflow or the action at the root of its repo.
func usedFrom(u gh.Use) string {
	if u.File == "action.yml" || u.File == "action.yaml" {
		return u.Repo
	}
	return sharedKey(u.Repo, u.File)
}

// collectShared gathers the shared workflows and actions, the most used
// first. runs are newest first; uses are what the workflow files use, read
// says whether they were read at all.
func collectShared(runs []repoRun, uses []gh.Use, read bool) (workflows, actions []*sharedItem) {
	items := map[string]*sharedItem{}
	get := func(repo, p string, action bool) *sharedItem {
		key := sharedKey(repo, p)
		if it, ok := items[key]; ok {
			return it
		}
		it := &sharedItem{repo: repo, path: p, action: action}
		items[key] = it
		return it
	}
	for i := range runs {
		x := &runs[i]
		seen := map[string]bool{}
		for _, ref := range x.run.ReferencedWorkflows {
			repo, file, version := ref.Split()
			key := sharedKey(repo, file)
			if repo == "" || seen[key] {
				continue
			}
			seen[key] = true
			it := get(repo, file, false)
			it.calls = append(it.calls, sharedCall{x, plainRef(version)})
		}
	}
	if read {
		targets := map[string]bool{}
		for _, u := range uses {
			targets[strings.ToLower(u.Target)] = true
		}
		for _, u := range uses {
			it := get(u.TargetRepo(), u.TargetFile(), !u.Workflow())
			if from := usedFrom(u); targets[from] {
				if !slices.Contains(it.inside, from) {
					it.inside = append(it.inside, from)
				}
				continue
			}
			it.addUser(plainRef(u.Version), sharedUser{u.Repo, u.File})
		}
	} else {
		for _, it := range items {
			seen := map[string]bool{}
			for _, c := range it.calls {
				if !seen[c.repo] {
					seen[c.repo] = true
					it.addUser(c.version, sharedUser{c.repo, c.run.Path})
				}
			}
		}
	}
	for _, it := range items {
		for _, from := range it.inside {
			if parent, ok := items[from]; ok && !it.action && !parent.action {
				parent.nested = append(parent.nested, it.key())
			}
		}
		it.finish()
		switch {
		case it.action && (len(it.versions) > 0 || len(it.inside) > 0):
			actions = append(actions, it)
		case !it.action && len(it.versions) > 0:
			workflows = append(workflows, it)
		}
	}
	return sortShared(workflows, "used"), sortShared(actions, "used")
}

// finish orders the versions, marks the ones behind and sums up the calls.
func (s *sharedItem) finish() {
	for _, c := range s.calls {
		if v := s.version(c.version); v != nil {
			v.calls = append(v.calls, c)
		}
	}
	sort.SliceStable(s.versions, func(i, j int) bool {
		a, b := versionParts(s.versions[i].version), versionParts(s.versions[j].version)
		if a == nil || b == nil {
			if a == nil && b == nil {
				return len(s.versions[i].users) > len(s.versions[j].users)
			}
			return a != nil
		}
		return newerTag(a, b)
	})
	for _, v := range s.versions {
		mine := versionParts(v.version)
		for _, other := range s.versions {
			if theirs := versionParts(other.version); mine != nil && theirs != nil && compareVersions(mine, theirs) < 0 {
				v.behind, v.ahead = true, other.version
				break
			}
		}
	}
	slices.Sort(s.nested)
	s.pass = passingRate(callKinds(s.calls))
	s.usual = callMedian(s.calls)
}

// sortShared orders shared items by one of sharedOrders, then by name.
func sortShared(list []*sharedItem, order string) []*sharedItem {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch order {
		case "behind":
			if x, y := a.reposBehind(), b.reposBehind(); x != y {
				return x > y
			}
		case "failing":
			if x, y := a.failing(), b.failing(); x != y {
				return x
			}
			if a.pass != b.pass {
				return a.pass < b.pass
			}
		case "last":
			x, okA := lastCall(a.calls, "")
			y, okB := lastCall(b.calls, "")
			if okA != okB {
				return okA
			}
			if okA && !x.run.CreatedAt.Equal(y.run.CreatedAt) {
				return x.run.CreatedAt.After(y.run.CreatedAt)
			}
		case "name":
			return a.name() < b.name()
		}
		if x, y := a.repoCount(), b.repoCount(); x != y {
			return x > y
		}
		return a.name() < b.name()
	})
	return list
}

// sortByGroup keeps the order of list but brings the items of one source
// repo together, the groups in the order their first item had.
func sortByGroup(list []*sharedItem) {
	rank := map[string]int{}
	for i, it := range list {
		if _, ok := rank[strings.ToLower(it.repo)]; !ok {
			rank[strings.ToLower(it.repo)] = i
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		return rank[strings.ToLower(list[i].repo)] < rank[strings.ToLower(list[j].repo)]
	})
}

// reposBehind counts the repos on a version some other repo is ahead of.
func (s *sharedItem) reposBehind() int {
	n := 0
	for _, v := range s.versions {
		if v.behind {
			n += len(v.users)
		}
	}
	return n
}

func (s *sharedItem) failing() bool {
	c, ok := lastCall(s.calls, "")
	return ok && StatusKind(c.run.Status, c.run.Conclusion) == KFail
}

// sharedQuery is a filter on shared items: words that each must match. A
// word is plain text, matched against the name, the source, the version and
// the repos using it, or one of repo:, source:, version: and is: (behind,
// failing, pinned).
type sharedQuery []sharedTerm

type sharedTerm struct{ field, value string }

func parseSharedQuery(q string) sharedQuery {
	var out sharedQuery
	for _, word := range strings.Fields(strings.ToLower(q)) {
		field, value, ok := strings.Cut(word, ":")
		switch {
		case ok && slices.Contains([]string{"repo", "source", "version", "is"}, field):
			out = append(out, sharedTerm{field, value})
		default:
			out = append(out, sharedTerm{"", word})
		}
	}
	return out
}

// match says whether a version line of it passes the query, and which of the
// repos on it a term matched.
func (q sharedQuery) match(a *App, it *sharedItem, ver *sharedVersion) ([]string, bool) {
	var repos []string
	for _, t := range q {
		name := strings.Contains(strings.ToLower(it.name()), t.value)
		source := strings.Contains(strings.ToLower(it.repo), t.value)
		version := strings.Contains(strings.ToLower(ver.version), t.value)
		users := ver.usersMatching(t.value)
		var ok bool
		switch t.field {
		case "":
			ok = name || source || version || len(users) > 0
		case "repo":
			ok = len(users) > 0
		case "source":
			ok = source
		case "version":
			ok = version
		case "is":
			ok = ver.is(t.value)
		}
		if !ok {
			return nil, false
		}
		if t.field == "repo" || (t.field == "" && !name && !source && !version) {
			repos = append(repos, users...)
		}
	}
	return repos, true
}

func (v *sharedVersion) usersMatching(text string) []string {
	var out []string
	for _, u := range v.users {
		if strings.Contains(strings.ToLower(u.repo), text) {
			out = append(out, u.repo)
		}
	}
	return out
}

// is answers is:behind, is:failing and is:pinned for a version line.
func (v *sharedVersion) is(state string) bool {
	switch state {
	case "behind":
		return v.behind
	case "failing":
		c, ok := lastCall(v.calls, "")
		return ok && StatusKind(c.run.Status, c.run.Conclusion) == KFail
	case "pinned":
		return refKind(v.version) != ""
	}
	return false
}

// sharedHeld is what we know of the shared workflows and actions.
type sharedHeld struct {
	workflows, actions []*sharedItem
	byKey              map[string]*sharedItem
	repos              int  // the repos it comes from
	fromFiles          bool // who uses what comes from the workflow files
}

// sharedCache holds sharedHeld, worked out once for every change to the
// runs, the workflow files read or the saved filter.
type sharedCache struct {
	valid   bool
	runsGen int
	usesGen int
	filter  string
	held    sharedHeld
}

func heldShared(a *App) sharedHeld {
	c := &a.st.shared
	filter := fmt.Sprint(a.filtering(), "\x00", a.cfg.RunFilter, "\x00", a.me)
	if c.valid && c.runsGen == a.st.runsGen && c.usesGen == a.st.usesGen && c.filter == filter {
		return c.held
	}
	runs := recentRuns(a, slices.Sorted(maps.Keys(a.st.status)))
	read := !a.st.usesState.at.IsZero()
	held := sharedHeld{fromFiles: read, repos: a.st.usesRepos, byKey: map[string]*sharedItem{}}
	held.workflows, held.actions = collectShared(runs, a.st.uses, read)
	if !read {
		repos := map[string]bool{}
		for _, x := range runs {
			repos[x.repo] = true
		}
		held.repos = len(repos)
	}
	for _, it := range append(slices.Clone(held.workflows), held.actions...) {
		held.byKey[it.key()] = it
	}
	*c = sharedCache{valid: true, runsGen: a.st.runsGen, usesGen: a.st.usesGen, filter: filter, held: held}
	return held
}

// sharedUsedBy are the shared workflows repo uses.
func sharedUsedBy(a *App, repo string) []*sharedItem {
	var out []*sharedItem
	for _, s := range heldShared(a).workflows {
		if s.usedBy(repo) {
			out = append(out, s)
		}
	}
	return out
}

var (
	versionTag = regexp.MustCompile(`^v?\d+(\.\d+)*$`)
	commitSHA  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// plainRef drops the refs/heads/ or refs/tags/ in front of a version.
func plainRef(version string) string {
	return strings.TrimPrefix(strings.TrimPrefix(version, "refs/heads/"), "refs/tags/")
}

// shortVersion is a version as shown: a commit by its first seven characters.
func shortVersion(version string) string {
	if commitSHA.MatchString(version) {
		return version[:7]
	}
	return version
}

// versionParts are the numbers of a tag such as v3 or v1.4.2, or nil for a
// branch or a commit.
func versionParts(version string) []int {
	if !versionTag.MatchString(version) || commitSHA.MatchString(version) {
		return nil
	}
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(version, "v"), ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// newerTag orders tags newest first; a floating tag such as v3 comes before
// the releases it stands for, as it moves to the newest of them.
func newerTag(a, b []int) bool {
	if c := compareVersions(a, b); c != 0 {
		return c > 0
	}
	return len(a) < len(b)
}

// compareVersions compares two tags as far as both go, so a floating v3 is
// level with v3.2.1.
func compareVersions(a, b []int) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

func repoCount(n int) string {
	if n == 1 {
		return "1 repo"
	}
	return fmt.Sprintf("%d repos", n)
}

func versionText(version string) string {
	if version == "" {
		return sDim.Render("default branch")
	}
	return fg(cRef).Render(shortVersion(version))
}
