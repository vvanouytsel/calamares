package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// backoff is a polling interval that doubles, up to max, each time a poll
// finds nothing new, and drops back to base as soon as something changes.
type backoff struct {
	base, max time.Duration
	cur       time.Duration
}

func newBackoff(base, max time.Duration) backoff { return backoff{base: base, max: max, cur: base} }

func (b *backoff) seen(changed bool) {
	if changed {
		b.cur = b.base
		return
	}
	b.cur = min(b.cur*2, b.max)
}

func (b *backoff) every() time.Duration { return b.cur }

// liveEvery is the fastest a screen with running work polls, from the config.
func (a *App) liveEvery() time.Duration {
	return time.Duration(a.cfg.RefreshSeconds) * time.Second
}

// maxLiveEvery is the slowest a screen with running work polls when nothing changes.
const maxLiveEvery = 30 * time.Second

func runsFingerprint(runs []gh.Run) string {
	var b strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&b, "%d:%s:%s:%d;", r.ID, r.Status, r.Conclusion, r.RunAttempt)
	}
	return b.String()
}

func jobsFingerprint(jobs []gh.Job) string {
	var b strings.Builder
	for _, j := range jobs {
		fmt.Fprintf(&b, "%d:%s:%s:", j.ID, j.Status, j.Conclusion)
		for _, s := range j.Steps {
			b.WriteString(s.Status[:min(len(s.Status), 1)])
		}
		b.WriteByte(';')
	}
	return b.String()
}
