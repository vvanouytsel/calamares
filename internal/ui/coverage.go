package ui

import (
	"strconv"
	"strings"
	"time"
)

// Actions covers every repo with workflows. How often a repo's runs are read
// depends on how much goes on in it: a busy repo every 15 seconds, one worked
// on lately every 2 minutes, one untouched for a day or more every 20
// minutes. Reading a repo whose runs did not change costs nothing against
// the rate limit (GitHub answers 304 to the ETag we send), so the untouched
// ones are cheap, and a push seen by the push check reads a repo at once.

// readGroup is how much goes on in a repo, which sets how often it is read.
type readGroup int

const (
	readBusy      readGroup = iota // a run is going
	readLately                     // pinned, watched, pushed to or run by you lately, a schedule just came due
	readUntouched                  // nothing of the above for a day or more
)

// The words for each group, as the coverage line and the repo panel say them.
var readGroupWords = []string{"busy", "worked on lately", "untouched for a day or more"}

const (
	busyEvery      = 15 * time.Second
	latelyEvery    = 2 * time.Minute
	untouchedEvery = 20 * time.Minute

	// latelyPush and latelyMine are how long a push, or a run of yours, keeps
	// a repo worked on lately.
	latelyPush = 24 * time.Hour
	latelyMine = 7 * 24 * time.Hour
	// scheduleWake is how long after a schedule came due its repo counts as
	// worked on lately: GitHub often starts a scheduled run many minutes late.
	scheduleWake = 30 * time.Minute

	// pushesEvery is how often the push check reads when your repos were
	// last pushed to: a GraphQL point per 100 repos.
	pushesEvery = 2 * time.Minute
)

// readInfo is the group of a repo and why it is in it.
type readInfo struct {
	group readGroup
	why   string
}

// readCache holds the group of each repo for a second: the list, its due
// checks and the coverage line all ask, for every repo, many times a second.
type readCache struct {
	at     time.Time
	byRepo map[string]readInfo
	index  map[string]int // a.repos by lower-case name
}

// fresh starts the cache over when it is a second old.
func (c *readCache) fresh(a *App, now time.Time) {
	if c.byRepo != nil && now.Sub(c.at) < time.Second && !now.Before(c.at) {
		return
	}
	c.at, c.byRepo, c.index = now, map[string]readInfo{}, make(map[string]int, len(a.repos))
	for i, r := range a.repos {
		c.index[strings.ToLower(r.FullName)] = i
	}
}

// pushedAt is when repo was last pushed to, as far as we know.
func (a *App) pushedAt(repo string, now time.Time) time.Time {
	a.reads.fresh(a, now)
	if i, ok := a.reads.index[strings.ToLower(repo)]; ok {
		return a.repos[i].PushedAt
	}
	return time.Time{}
}

// readInfoOf says how much goes on in repo, and why.
func (a *App) readInfoOf(repo string, now time.Time) readInfo {
	a.reads.fresh(a, now)
	key := strings.ToLower(repo)
	if info, ok := a.reads.byRepo[key]; ok {
		return info
	}
	info := a.readInfoNow(repo, now)
	a.reads.byRepo[key] = info
	return info
}

func (a *App) readInfoNow(repo string, now time.Time) readInfo {
	if repoActive(a, repo) {
		return readInfo{readBusy, "a run is going"}
	}
	if a.cfg.IsFavourite(repo) {
		return readInfo{readLately, "pinned"}
	}
	if _, ok := a.cfg.RepoWatch(repo); ok {
		return readInfo{readLately, "watched"}
	}
	pushed := a.pushedAt(repo, now)
	if !pushed.IsZero() && now.Sub(pushed) < latelyPush {
		return readInfo{readLately, "pushed " + age(pushed, now) + " ago"}
	}
	if at, ok := a.scheduleCameDue(repo, now); ok {
		return readInfo{readLately, "a schedule came due " + age(at, now) + " ago"}
	}
	if a.st.activeIn(repo, a.me, now.Add(-latelyMine)) {
		return readInfo{readLately, "you ran something this week"}
	}
	if pushed.IsZero() {
		return readInfo{readUntouched, "no push known"}
	}
	return readInfo{readUntouched, "no push for " + age(pushed, now)}
}

// readEvery is how often a repo in group g is read. While the requests left
// this hour run low, the quieter groups wait longer; a busy repo never does.
func (a *App) readEvery(g readGroup) time.Duration {
	switch g {
	case readBusy:
		return busyEvery
	case readLately:
		return latelyEvery * time.Duration(a.readStretch())
	}
	return untouchedEvery * time.Duration(a.readStretch())
}

// readStretch is how many times longer the quieter groups wait: twice as
// long once less than half of the hour's requests are left, three times
// below a quarter.
func (a *App) readStretch() int {
	r := a.client.Rate()
	if r.Limit <= 0 {
		return 1
	}
	switch left := r.Remaining * 100 / r.Limit; {
	case left < 25:
		return 3
	case left < 50:
		return 2
	}
	return 1
}

// readDue reports whether repo, last read as l says, is due to be read again:
// it was never read, it was pushed to since, or its group's time has passed.
// A read is never repeated within floor.
func (a *App) readDue(l *loadState, repo string, now time.Time, floor time.Duration) bool {
	if l.tried.IsZero() {
		return true
	}
	since := now.Sub(l.tried)
	if since >= busyEvery && since >= floor && a.pushedAt(repo, now).After(l.tried) {
		return true
	}
	return since >= max(a.readEvery(a.readInfoOf(repo, now).group), floor)
}

// readGroups counts repos per group.
func (a *App) readGroups(repos []string, now time.Time) [3]int {
	var n [3]int
	for _, repo := range repos {
		n[a.readInfoOf(repo, now).group]++
	}
	return n
}

// scheduleCameDue reports when a schedule of repo last came due, if that was
// within scheduleWake.
func (a *App) scheduleCameDue(repo string, now time.Time) (time.Time, bool) {
	var last time.Time
	for _, s := range a.st.schedules {
		if !strings.EqualFold(s.Repo, repo) {
			continue
		}
		for _, c := range s.Crons {
			if at, ok := cronLastDue(c, now, scheduleWake); ok && at.After(last) {
				last = at
			}
		}
	}
	return last, !last.IsZero()
}

// cronLastDue is the last minute within window before now that cron came
// due. GitHub reads cron lines in UTC.
func cronLastDue(cron string, now time.Time, window time.Duration) (time.Time, bool) {
	spec, ok := parseCron(cron)
	if !ok {
		return time.Time{}, false
	}
	t := now.UTC().Truncate(time.Minute)
	for end := now.Add(-window); !t.Before(end); t = t.Add(-time.Minute) {
		if spec.matches(t) {
			return t, true
		}
	}
	return time.Time{}, false
}

// cronSpec is a parsed cron line: for each field, the values it allows.
type cronSpec struct {
	minute, hour, dom, month, dow uint64
	anyDom, anyDow                bool
}

// parseCron reads a cron line of five fields: numbers, *, ranges (1-5),
// steps (*/15, 0-30/10), lists (1,15) and the names of months and days.
func parseCron(cron string) (cronSpec, bool) {
	f := strings.Fields(cron)
	if len(f) != 5 {
		return cronSpec{}, false
	}
	months := []string{"", "JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"}
	days := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	var s cronSpec
	var ok [5]bool
	s.minute, ok[0] = cronField(f[0], 0, 59, nil)
	s.hour, ok[1] = cronField(f[1], 0, 23, nil)
	s.dom, ok[2] = cronField(f[2], 1, 31, nil)
	s.month, ok[3] = cronField(f[3], 1, 12, months)
	s.dow, ok[4] = cronField(f[4], 0, 7, days)
	for _, o := range ok {
		if !o {
			return cronSpec{}, false
		}
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 // 7 is Sunday too
	}
	s.anyDom, s.anyDow = f[2] == "*", f[4] == "*"
	return s, true
}

// cronField reads one field of a cron line into a set of bits.
func cronField(field string, lo, hi int, names []string) (uint64, bool) {
	num := func(s string) (int, bool) {
		for i, n := range names {
			if n != "" && strings.EqualFold(s, n) {
				return i, true
			}
		}
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= lo && n <= hi
	}
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return 0, false
			}
			step = n
		}
		from, to := lo, hi
		if rng != "*" {
			a, b, isRange := strings.Cut(rng, "-")
			var okA, okB bool
			if from, okA = num(a); !okA {
				return 0, false
			}
			to = from
			if isRange {
				if to, okB = num(b); !okB || to < from {
					return 0, false
				}
			} else if hasStep {
				to = hi
			}
		}
		for v := from; v <= to; v += step {
			bits |= 1 << v
		}
	}
	return bits, true
}

// matches reports whether the minute t falls on the cron line. When both the
// day of the month and the day of the week are set, either one will do.
func (s cronSpec) matches(t time.Time) bool {
	if s.minute&(1<<t.Minute()) == 0 || s.hour&(1<<t.Hour()) == 0 || s.month&(1<<int(t.Month())) == 0 {
		return false
	}
	dom, dow := s.dom&(1<<t.Day()) != 0, s.dow&(1<<int(t.Weekday())) != 0
	if s.anyDom || s.anyDow {
		return dom && dow
	}
	return dom || dow
}

// coverageWords says how many of repos are in each group: "9 busy · 27
// worked on lately · 176 untouched for a day or more", leaving out empty
// groups. While the requests left run low the line is amber.
func (a *App) coverageWords(repos []string, now time.Time) string {
	n := a.readGroups(repos, now)
	var parts []string
	for g, c := range n {
		if c > 0 {
			parts = append(parts, sText.Render(strconv.Itoa(c))+" "+sDim.Render(readGroupWords[g]))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	dot := sGreen.Render("●")
	if a.readStretch() > 1 {
		dot = sAmber.Render("●")
	}
	return dot + " " + strings.Join(parts, sDim.Render(" · "))
}

// readWords says how often Actions reads repo and why, for its panel:
// "every 2 min · worked on lately: pushed 3h ago", and for an untouched repo
// also when it was last read.
func (a *App) readWords(repo string, now time.Time) string {
	info := a.readInfoOf(repo, now)
	s := sText.Render("every "+everyWords(a.readEvery(info.group))) + sDim.Render(" · "+readGroupWords[info.group]+": "+info.why)
	if at := a.st.runState(repo).at; info.group == readUntouched && !at.IsZero() {
		s += sDim.Render(" · last read " + age(at, now) + " ago")
	}
	return s
}

// everyWords says an interval of reading: "15 s", "2 min", "1 h".
func everyWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + " s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	}
	return strconv.Itoa(int(d.Hours())) + " h"
}
