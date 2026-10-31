package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// releaseApp holds a fresh v4 of build.yml that fails in half of its calls
// and a v3 before it that passes them all.
func releaseApp(t *testing.T, v4 ...string) *App {
	now := time.Now()
	a := sharedApp(t)
	const buildV4 = "acme/workflows/.github/workflows/build.yml@v4"
	var api, web []gh.Run
	for i, c := range v4 {
		api = append(api, callingRun(int64(100+i), c, now.Add(-time.Duration(i+1)*time.Hour), buildV4))
	}
	for i := range 8 {
		web = append(web, callingRun(int64(200+i), "success", now.Add(-time.Duration(30+i)*time.Hour), buildV3))
	}
	a.st.status["acme/api"] = api
	a.st.status["acme/web"] = web
	return a
}

func buildItem(t *testing.T, a *App) *sharedItem {
	t.Helper()
	it, ok := heldShared(a).byKey["acme/workflows/.github/workflows/build.yml"]
	if !ok {
		t.Fatal("build.yml is not held")
	}
	return it
}

func TestANewVersionThatFailsMoreIsWorseThanTheOneBefore(t *testing.T) {
	a := releaseApp(t, "failure", "success", "failure", "success", "failure", "success")
	it := buildItem(t, a)
	c, ok := it.newCheck()
	if !ok {
		t.Fatal("v4 is not new")
	}
	if c.prev == nil || c.prev.version != "v3" {
		t.Fatalf("previous = %+v, want v3", c.prev)
	}
	if c.verdict != verdictWorse || c.pass != 50 || c.prevPass != 100 {
		t.Errorf("verdict %d, %d%% against %d%%; want worse, 50 against 100", c.verdict, c.pass, c.prevPass)
	}
	_, out := sharedTab(a, sharedFilter)
	wantAll(t, out, "v4 latest", "1 failing version", "Fails more than v3", "3 of 6")
}

func TestTooFewCallsAreTooEarlyAndAGoodOneIsFine(t *testing.T) {
	early := buildItem(t, releaseApp(t, "failure", "failure"))
	if c, _ := early.newCheck(); c.verdict != verdictEarly {
		t.Errorf("two calls: verdict %d, want too early", c.verdict)
	}
	fine := buildItem(t, releaseApp(t, "success", "success", "success", "success", "success"))
	if c, _ := fine.newCheck(); c.verdict != verdictFine {
		t.Errorf("five passes: verdict %d, want fine", c.verdict)
	}
}

func TestAVersionWithoutAnOlderTagHasNothingToCompare(t *testing.T) {
	a := sharedApp(t)
	it := heldShared(a).byKey["acme/security/.github/workflows/lint.yml"]
	if it == nil {
		t.Fatal("lint.yml is not held")
	}
	if _, ok := it.newCheck(); ok {
		t.Error("a commit has no version before it")
	}
	if c := it.check(it.versions[0]); c.prev != nil {
		t.Errorf("a commit was compared with %s", c.prev.version)
	}
}

func TestTheVersionPanelComparesAndNamesFailuresOnlyTheNewOneHas(t *testing.T) {
	a := releaseApp(t, "failure", "success", "failure", "success", "failure", "success")
	for _, r := range a.st.status["acme/api"] {
		if r.Conclusion == "failure" {
			a.st.causes[causeID("acme/api", r)] = runCause{Text: "setup-node: Unsupported engine \"node\" 18"}
		}
	}
	a.st.diffs[diffKey("acme/workflows", "v3", "v4")] = &versionDiff{commits: 12, bumps: []string{"setup-node v3 → v4"}}
	v, _ := sharedTab(a, sharedFilter)
	for i := 0; i < 5; i++ {
		if _, ver := v.shared.selected(); ver != nil && ver.version == "v4" {
			break
		}
		v.shared.t.SetCursor(v.shared.t.Cursor() + 1)
	}
	out := ansi.Strip(strings.Join(v.shared.panel(a, 60, 60), "\n"))
	wantAll(t, out, "Fails more than v3", "Passed", "3 of 6 runs", "On v3", "New failures in v4", "Unsupported engine", "Seen in 3 runs",
		"Changes from v3 to v4", "12 commits", "setup-node v3 → v4", "Diff v3 → v4")
	found := false
	for _, b := range v.Keys(a) {
		for _, k := range b.Keys {
			if k == "d" {
				found = b.Desc == "diff v3 → v4"
			}
		}
	}
	if !found {
		t.Error("no d diff v3 → v4 key")
	}
}

func TestUsesBumpsPairTheLinesAPatchChanges(t *testing.T) {
	patch := strings.Join([]string{
		"@@ -10,7 +10,7 @@",
		"-      - uses: actions/setup-node@v3",
		"+      - uses: actions/setup-node@v4",
		"       - uses: actions/checkout@v4",
		"-    uses: acme/workflows/.github/workflows/helper.yml@v1",
		"+    uses: acme/workflows/.github/workflows/helper.yml@v1",
	}, "\n")
	got := usesBumps(patch)
	if len(got) != 1 || got[0] != "setup-node v3 → v4" {
		t.Errorf("bumps = %q, want only setup-node v3 → v4", got)
	}
}

func TestJobNamesDropTheCallingJob(t *testing.T) {
	for in, want := range map[string]string{"build / test (ubuntu)": "test (ubuntu)", "lint": "lint", "ci / build / image": "image"} {
		if got := jobName(in); got != want {
			t.Errorf("jobName(%q) = %q, want %q", in, got, want)
		}
	}
}
