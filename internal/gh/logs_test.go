package gh

import (
	"testing"
	"time"
)

const sampleLog = bom + "2026-09-23T10:19:33.5357959Z Current runner version: '2.337.0'\r\n" +
	"2026-09-23T10:19:33.5386273Z ##[group]Runner Image Provisioner\r\n" +
	"2026-09-23T10:19:33.5391844Z ##[endgroup]\r\n" +
	"2026-09-23T10:19:34.1000000Z Download action repository 'acme/checkout@v4'\r\n" +
	"2026-09-23T10:19:34.2000000Z ##[group]Run acme/checkout@v4\r\n" +
	"2026-09-23T10:19:34.3000000Z Syncing repository\r\n" +
	"2026-09-23T10:19:46.1000000Z ##[group]Run go test ./...\r\n" +
	"2026-09-23T10:19:50.0000000Z --- FAIL: TestRefund (0.04s)\r\n" +
	"2026-09-23T10:19:52.0000000Z ##[error]Process completed with exit code 1.\r\n"

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseLog(t *testing.T) {
	lines := ParseLog(sampleLog)
	if len(lines) != 9 {
		t.Fatalf("got %d lines, want 9", len(lines))
	}
	if lines[0].Text != "Current runner version: '2.337.0'" || lines[0].Time.IsZero() {
		t.Errorf("first line not parsed: %+v", lines[0])
	}
	if lines[1].Kind != LineGroup || lines[1].Text != "Runner Image Provisioner" {
		t.Errorf("group not detected: %+v", lines[1])
	}
	if lines[8].Kind != LineError || !IsErrorLine(lines[8]) || !IsGenericFailure(lines[8]) {
		t.Errorf("error line not detected: %+v", lines[8])
	}
	if !IsErrorLine(lines[7]) {
		t.Errorf("--- FAIL should be an error line")
	}
	if IsErrorLine(lines[5]) {
		t.Errorf("normal line flagged as error")
	}
}

func TestSplitByStep(t *testing.T) {
	steps := []Step{
		{Name: "Set up job", Number: 1, StartedAt: ts("2026-09-23T10:19:33Z"), CompletedAt: ts("2026-09-23T10:19:34Z")},
		{Name: "Checkout", Number: 2, StartedAt: ts("2026-09-23T10:19:34Z"), CompletedAt: ts("2026-09-23T10:19:46Z")},
		{Name: "Skipped", Number: 3},
		{Name: "Test", Number: 4, StartedAt: ts("2026-09-23T10:19:46Z"), CompletedAt: ts("2026-09-23T10:19:52Z")},
	}
	got := SplitByStep(ParseLog(sampleLog), steps)
	if len(got) != 4 {
		t.Fatalf("got %d steps, want 4", len(got))
	}
	want := []int{4, 2, 0, 3}
	for i, w := range want {
		if len(got[i].Lines) != w {
			t.Errorf("step %q: got %d lines, want %d", got[i].Step.Name, len(got[i].Lines), w)
		}
	}
	if got[1].Lines[0].Text != "Run acme/checkout@v4" {
		t.Errorf("checkout should start at its group line, got %q", got[1].Lines[0].Text)
	}
}

// Mirrors a real job where the last four steps all start in the same second
// and one of them was skipped (but still has timestamps).
func TestSplitByStepSameSecond(t *testing.T) {
	log := "2026-10-07T21:24:21.4751166Z ##[group]Runner Image Provisioner\n" +
		"2026-10-07T21:24:22.2694508Z ##[group]Run acme/checkout@v7\n" +
		"2026-10-07T21:24:24.0356607Z ##[group]Determining the checkout info\n" +
		"2026-10-07T21:24:24.2110889Z ##[group]Run actions/setup-node@v7\n" +
		"2026-10-07T21:24:28.3311231Z ##[group]Run npm run build\n" +
		"2026-10-07T21:24:40.1000000Z built in 12s\n" +
		"2026-10-07T21:24:40.3402945Z ##[group]Run if [ -n \"$(git status --porcelain)\" ]; then\n" +
		"2026-10-07T21:24:40.5707535Z ##[error]The committed web UI is out of date.\n" +
		"2026-10-07T21:24:40.5829677Z ##[error]Process completed with exit code 1.\n" +
		"2026-10-07T21:24:40.6000000Z Post job cleanup.\n" +
		"2026-10-07T21:24:40.7000000Z Cleaning up orphan processes\n"
	st := func(n int, name, start, end, concl string) Step {
		return Step{Number: n, Name: name, Status: "completed", Conclusion: concl, StartedAt: ts(start), CompletedAt: ts(end)}
	}
	steps := []Step{
		st(1, "Set up job", "2026-10-07T21:24:21Z", "2026-10-07T21:24:22Z", "success"),
		st(2, "Checkout", "2026-10-07T21:24:22Z", "2026-10-07T21:24:24Z", "success"),
		st(3, "Setup node", "2026-10-07T21:24:24Z", "2026-10-07T21:24:26Z", "success"),
		st(5, "Build", "2026-10-07T21:24:28Z", "2026-10-07T21:24:40Z", "success"),
		st(6, "Committed build matches source", "2026-10-07T21:24:40Z", "2026-10-07T21:24:40Z", "failure"),
		st(11, "Post setup node", "2026-10-07T21:24:40Z", "2026-10-07T21:24:40Z", "skipped"),
		st(12, "Post checkout", "2026-10-07T21:24:40Z", "2026-10-07T21:24:40Z", "success"),
		st(13, "Complete job", "2026-10-07T21:24:40Z", "2026-10-07T21:24:40Z", "success"),
	}
	got := SplitByStep(ParseLog(log), steps)
	want := map[string]int{"Set up job": 1, "Checkout": 2, "Setup node": 1, "Build": 2, "Committed build matches source": 3, "Post setup node": 0, "Post checkout": 1, "Complete job": 1}
	if len(got) != len(steps) {
		t.Fatalf("got %d steps, want %d", len(got), len(steps))
	}
	for _, s := range got {
		if len(s.Lines) != want[s.Step.Name] {
			t.Errorf("step %q: got %d lines, want %d", s.Step.Name, len(s.Lines), want[s.Step.Name])
		}
	}
}

func TestSplitByStepNoSteps(t *testing.T) {
	got := SplitByStep(ParseLog(sampleLog), nil)
	if len(got) != 1 || len(got[0].Lines) != 9 {
		t.Fatalf("expected a single step with every line, got %+v", got)
	}
}
