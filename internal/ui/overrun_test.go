package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/vvanouytsel/calamares/internal/gh"
)

func ciRun(id int64, status, conclusion string, started time.Time, took time.Duration) gh.Run {
	return gh.Run{ID: id, Name: "CI", RunNumber: int(id), Status: status, Conclusion: conclusion, HeadBranch: "main", RunAttempt: 1,
		CreatedAt: started, RunStartedAt: started, UpdatedAt: started.Add(took), DisplayTitle: "feat: gift cards"}
}

func TestARunTakingFarLongerThanUsualIsFlagged(t *testing.T) {
	a := keysApp(t, false)
	now := time.Now()
	a.repos = []gh.Repo{{FullName: "acme/webshop", HasActions: true, DefaultBranch: "main", PushedAt: now}}
	a.st.loaded = true
	runs := []gh.Run{
		ciRun(5, "in_progress", "", now.Add(-40*time.Minute), 0),
		ciRun(4, "in_progress", "", now.Add(-6*time.Minute), 0),
	}
	for i := int64(1); i <= 3; i++ {
		runs = append(runs, ciRun(i, "completed", "success", now.Add(-time.Duration(i)*time.Hour), 5*time.Minute))
	}
	a.st.status["acme/webshop"] = runs
	a.st.runState("acme/webshop").at = now
	if _, over := a.overrunning(repoRun{"acme/webshop", runs[0]}, now); !over {
		t.Error("40 minutes against a usual 5 is running long")
	}
	if _, over := a.overrunning(repoRun{"acme/webshop", runs[1]}, now); over {
		t.Error("6 minutes against a usual 5 is not")
	}
	v := newActivityView(a)
	v.sync(a)
	out := ansi.Strip(v.Render(a, 190, 30))
	wantAll(t, out, "1 running long", "2 running")
}
