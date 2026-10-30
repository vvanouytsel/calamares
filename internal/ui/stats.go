package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// runStats summarises a sample of runs (newest first).
type runStats struct {
	total, ok, fail, cancel, active int
	median, p90                     time.Duration
	streak                          int // consecutive finished runs with streakKind, newest first
	streakKind                      Kind
	streakSince                     *gh.Run
	lastFail                        *gh.Run
	lastOK                          *gh.Run
	events                          map[string]int
	failBranches                    map[string]int
	oldest                          time.Time
}

func computeStats(runs []gh.Run, now time.Time) runStats {
	s := runStats{events: map[string]int{}, failBranches: map[string]int{}}
	var durs []time.Duration
	streakOpen := true
	for i := range runs {
		r := &runs[i]
		s.total++
		s.events[r.Event]++
		if s.oldest.IsZero() || r.CreatedAt.Before(s.oldest) {
			s.oldest = r.CreatedAt
		}
		if r.Active() {
			s.active++
			continue
		}
		k := StatusKind(r.Status, r.Conclusion)
		switch k {
		case KOK:
			s.ok++
			durs = append(durs, r.Duration(now))
			if s.lastOK == nil {
				s.lastOK = r
			}
		case KFail:
			s.fail++
			s.failBranches[r.HeadBranch]++
			durs = append(durs, r.Duration(now))
			if s.lastFail == nil {
				s.lastFail = r
			}
		case KCancel:
			s.cancel++
		}
		if k != KOK && k != KFail {
			continue
		}
		if streakOpen {
			if s.streak == 0 {
				s.streakKind = k
			}
			if k == s.streakKind {
				s.streak++
				s.streakSince = r
			} else {
				streakOpen = false
			}
		}
	}
	if len(durs) > 0 {
		sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
		s.median = durs[len(durs)/2]
		s.p90 = durs[min(len(durs)*9/10, len(durs)-1)]
	}
	return s
}

// successRate is the share of finished (pass/fail) runs that passed, or -1.
func (s runStats) successRate() int {
	if s.ok+s.fail == 0 {
		return -1
	}
	return s.ok * 100 / (s.ok + s.fail)
}

func rateStyle(pct int) lipgloss.Style {
	switch {
	case pct < 0:
		return sDim
	case pct < 70:
		return sRed
	case pct < 90:
		return sAmber
	}
	return sGreen
}

// meter renders a horizontal bar of w cells filled to pct.
func meter(pct, w int) string {
	if pct < 0 {
		return sDim.Render(strings.Repeat("─", w))
	}
	on := (pct*w + 50) / 100
	return rateStyle(pct).Render(strings.Repeat("█", on)) + fg(cMeterOff).Render(strings.Repeat("█", w-on))
}

// topN lists the n biggest counts as "name 3 · other 1".
func topN(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
	var parts []string
	for i, e := range all {
		if i >= n {
			break
		}
		parts = append(parts, sText.Render(e.k)+" "+sDim.Render(strconv.Itoa(e.v)))
	}
	if len(parts) == 0 {
		return sDim.Render("—")
	}
	return strings.Join(parts, sDim.Render(" · "))
}
