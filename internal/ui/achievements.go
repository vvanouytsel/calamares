package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// Achievements are badges for what you do with Calamares. What is counted
// has up to four tiers (bronze, silver, gold, platinum); the rest is earned
// once, at one tier. Each tier is worth XP, and XP adds up to a level with a
// title. Progress is a few counters kept in achievements.json next to the
// config: what was counted, never the events themselves. Counting starts
// the day achievements arrive; nothing is worked out from the past.

type tier int

const (
	tierNone tier = iota
	tierBronze
	tierSilver
	tierGold
	tierPlatinum
)

var tierNames = [...]string{"", "bronze", "silver", "gold", "platinum"}

// tierXP is what reaching each tier is worth.
var tierXP = [...]int{0, 10, 25, 50, 100}

func (t tier) String() string { return tierNames[t] }

// area is the part of Calamares an achievement belongs to.
type area int

const (
	areaActions area = iota
	areaPulls
	areaWatching
	areaPlatform
	areaExplorer
	areaSecret
)

var (
	areaNames = [...]string{"Actions", "Pull requests", "Watching and habits", "Platform", "Explorer", "Secret"}
	areaIcons = [...]string{"actions", "pr", "watching", "platform", "compass", "lock"}
	// areaPlaces are the places enter goes to from an achievement; -1 for none.
	areaPlaces = [...]int{tabActions, tabPulls, tabWatching, tabPlatform, -1, -1}
	areaTips   = [...]string{
		"R reruns what broke, on Failing and on a run.",
		"a approves, C asks for changes, M merges, d sets a review aside.",
		"w watches a run, W on a repo watches its new runs.",
		"The shared workflows, warnings, runners and runner time live here.",
		"i explains any screen, . lists what you can do.",
		"Some things are found, not earned.",
	}
)

// color is the colour of an area's icons.
func (ar area) color() lipgloss.Color {
	switch ar {
	case areaActions:
		return cRef
	case areaPulls:
		return cViolet
	case areaWatching:
		return cTeal
	case areaPlatform:
		return cGold
	case areaExplorer:
		return cWait
	}
	return lipgloss.Color(mix(string(cViolet), string(cWhite), 0.4))
}

// counting is how an achievement's counter moves.
type counting int

const (
	countUp       counting = iota // adds up
	countBest                     // keeps the highest value seen
	countDistinct                 // counts different values
)

type achievement struct {
	id, name string
	area     area
	glyph    [2]string // the Nerd Font glyph and its plain stand-in
	text     string    // how to earn it
	quip     string
	hint     string // what a secret says before it is found
	// tiers are the counts each tier needs, bronze first; a one-off has one,
	// worth medal.
	tiers []int
	medal tier
	how   counting
}

func (x *achievement) secret() bool { return x.area == areaSecret }
func (x *achievement) oneOff() bool { return len(x.tiers) == 1 }

// top is the highest tier it has.
func (x *achievement) top() tier {
	if x.oneOff() {
		return x.medal
	}
	return tier(len(x.tiers))
}

// tierAt is the tier a count reaches.
func (x *achievement) tierAt(n int) tier {
	reached := 0
	for _, need := range x.tiers {
		if n >= need {
			reached++
		}
	}
	switch {
	case reached == 0:
		return tierNone
	case x.oneOff():
		return x.medal
	}
	return tier(reached)
}

// next is the count the next tier needs and that tier; 0 once every tier
// is reached.
func (x *achievement) next(n int) (int, tier) {
	for i, need := range x.tiers {
		if n < need {
			if x.oneOff() {
				return need, x.medal
			}
			return need, tier(i + 1)
		}
	}
	return 0, tierNone
}

// xpUpTo is what reaching t is worth in all: every tier below it too.
func (x *achievement) xpUpTo(t tier) int {
	if x.oneOff() {
		if t == tierNone {
			return 0
		}
		return tierXP[x.medal]
	}
	xp := 0
	for i := tierBronze; i <= t; i++ {
		xp += tierXP[i]
	}
	return xp
}

// glyphText is the achievement's glyph, or "" when the icons have none.
func (x *achievement) glyphText() string { return glyphOf(x.glyph) }

func glyphOf(g [2]string) string {
	s := g[1]
	if nerd {
		s = g[0]
	}
	if ascii {
		return ""
	}
	return s
}

// once is the tiers of a one-off earned the first time.
var once = []int{1}

// achievements are every achievement, in the order of the trophy case.
var achievements = []*achievement{
	{id: "first-light", name: "First light", area: areaActions, glyph: [2]string{"", "☼"}, tiers: once, medal: tierBronze,
		text: "Watch a run you picked go green.", quip: "Every pipeline starts somewhere."},
	{id: "second-wind", name: "Second wind", area: areaActions, glyph: [2]string{"", "↻"}, tiers: []int{1, 10, 50, 200},
		text: "Rerun a failed run and watch it pass.", quip: "Turning it off and on again, professionally."},
	{id: "green-again", name: "Green again", area: areaActions, glyph: [2]string{"", "♣"}, tiers: []int{1, 10, 50, 200},
		text: "Watch a run bring a default branch back to green after it broke.", quip: "Gardener of main."},
	{id: "flake-hunter", name: "Flake hunter", area: areaActions, glyph: [2]string{"", "✱"}, tiers: []int{1, 5, 25, 100},
		text: "Watch a run pass on its third attempt or later.", quip: "It's not you, it's the network."},
	{id: "speed-demon", name: "Speed demon", area: areaActions, glyph: [2]string{"", "ϟ"}, tiers: once, medal: tierSilver,
		text: "Watch a run pass in under 70% of its usual time.", quip: "Somebody warmed the cache."},
	{id: "slow-cooker", name: "Slow cooker", area: areaActions, glyph: [2]string{"", "⧗"}, tiers: once, medal: tierBronze,
		text: "Watch a run of more than an hour to the very end.", quip: "Patience, compiled."},
	{id: "pipeline-whisperer", name: "Pipeline whisperer", area: areaActions, glyph: [2]string{"", "♫"}, tiers: []int{10, 100, 500, 2000},
		text: "Watch runs to the end.", quip: "They talk to you now."},
	{id: "mass-revival", name: "Mass revival", area: areaActions, glyph: [2]string{"", "✚"}, tiers: []int{10}, medal: tierGold, how: countBest,
		text: "Rerun the failed jobs of ten runs or more in one go.", quip: "Necromancy, but for CI."},
	{id: "root-cause", name: "Root cause", area: areaActions, glyph: [2]string{"", "◎"}, tiers: []int{5, 25, 100},
		text: "Open the log of a failed run straight from its card with ctrl+l.", quip: "ctrl+l is a lifestyle."},
	{id: "debug-mode", name: "Debug mode", area: areaActions, glyph: [2]string{"", ">"}, tiers: once, medal: tierBronze,
		text: "Rerun with debug logging on.", quip: "Set the flag and pray."},
	{id: "no-spoon", name: "There is no spoon", area: areaActions, glyph: [2]string{"", "▦"}, tiers: once, medal: tierGold,
		text: "Watch a run of 20 jobs or more pass.", quip: "You saw the matrix. It was green."},
	{id: "clean-sweep", name: "Clean sweep", area: areaActions, glyph: [2]string{"", "✦"}, tiers: once, medal: tierGold,
		text: "See Failing empty across every repo.", quip: "Nothing to see here. Literally."},
	{id: "time-lord", name: "Time lord", area: areaActions, glyph: [2]string{"", "◷"}, tiers: []int{1, 5, 25}, how: countDistinct,
		text: "Spot workflows on Time whose runner time grew by half in a week.", quip: "Where did all the minutes go?"},
	{id: "cancel-culture", name: "Cancel culture", area: areaActions, glyph: [2]string{"", "⊘"}, tiers: []int{1, 10, 50},
		text: "Cancel runs that were no longer needed.", quip: "Not every run deserves to finish."},

	{id: "merge-train", name: "Merge train", area: areaPulls, glyph: [2]string{"", "≡"}, tiers: []int{5}, medal: tierGold, how: countBest,
		text: "Merge five pull requests in one day.", quip: "Choo choo."},
	{id: "merge-master", name: "Merge master", area: areaPulls, glyph: [2]string{"", "⑂"}, tiers: []int{10, 50, 250, 1000},
		text: "Merge pull requests.", quip: "Main is a busy street."},
	{id: "nitpicker", name: "Nitpicker", area: areaPulls, glyph: [2]string{"", "✎"}, tiers: []int{1, 10, 50},
		text: "Request changes on pull requests.", quip: "Nit: missing full stop."},
	{id: "rubber-stamp", name: "Rubber stamp", area: areaPulls, glyph: [2]string{"", "✓"}, tiers: []int{1, 25, 100, 500},
		text: "Approve pull requests.", quip: "Looks good to me. Probably."},
	{id: "second-opinion", name: "Second opinion", area: areaPulls, glyph: [2]string{"", "❝"}, tiers: []int{1, 10, 50, 250},
		text: "Comment on pull requests.", quip: "Have you considered…"},
	{id: "bulk-approver", name: "Bulk approver", area: areaPulls, glyph: [2]string{"", "☑"}, tiers: []int{5}, medal: tierSilver, how: countBest,
		text: "Approve five pull requests or more in one go.", quip: "Efficient. Or reckless."},
	{id: "out-of-drafts", name: "Out of the drafts", area: areaPulls, glyph: [2]string{"", "✐"}, tiers: once, medal: tierBronze,
		text: "Mark a draft ready for review.", quip: "It grew up so fast."},
	{id: "spring-cleaning", name: "Spring cleaning", area: areaPulls, glyph: [2]string{"", "⌫"}, tiers: once, medal: tierSilver,
		text: "Merge a pull request that removes at least 100 more lines than it adds.", quip: "Less code, fewer bugs."},
	{id: "big-bang", name: "Big bang", area: areaPulls, glyph: [2]string{"", "✸"}, tiers: once, medal: tierSilver,
		text: "Merge a pull request of more than 1,000 changed lines.", quip: "Approved by sheer willpower."},
	{id: "one-liner", name: "One-liner", area: areaPulls, glyph: [2]string{"", "—"}, tiers: once, medal: tierGold,
		text: "Merge a pull request that changes a single line.", quip: "The best diff is a small diff."},
	{id: "not-now", name: "Not now", area: areaPulls, glyph: [2]string{"", "‖"}, tiers: []int{1, 10, 50},
		text: "Set a review or an issue aside with d.", quip: "Future you will handle it."},

	{id: "eagle-eye", name: "Eagle eye", area: areaWatching, glyph: [2]string{"", "◉"}, tiers: once, medal: tierBronze,
		text: "Watch your first run or repo.", quip: "Nothing escapes you now."},
	{id: "lighthouse-keeper", name: "Lighthouse keeper", area: areaWatching, glyph: [2]string{"", "⌖"}, tiers: []int{1, 5, 10, 25}, how: countBest,
		text: "Watch repos with W: how many at once.", quip: "Steady light, many ships."},
	{id: "watch-party", name: "Watch party", area: areaWatching, glyph: [2]string{"", "☺"}, tiers: []int{5}, medal: tierGold, how: countBest,
		text: "Watch five runs at once while they run.", quip: "Popcorn is on the runner."},
	{id: "night-owl", name: "Night owl", area: areaWatching, glyph: [2]string{"", "☾"}, tiers: once, medal: tierBronze,
		text: "Watch a run finish between midnight and five.", quip: "Go to bed."},
	{id: "early-bird", name: "Early bird", area: areaWatching, glyph: [2]string{"", "◔"}, tiers: once, medal: tierBronze,
		text: "Open Calamares between five and seven in the morning.", quip: "The pipeline was up first. Barely."},
	{id: "weekend-warrior", name: "Weekend warrior", area: areaWatching, glyph: [2]string{"", "☂"}, tiers: once, medal: tierSilver,
		text: "Merge a pull request on a Saturday or Sunday.", quip: "Touch grass. After this merge."},
	{id: "marathon", name: "Marathon", area: areaWatching, glyph: [2]string{"", "∞"}, tiers: once, medal: tierSilver,
		text: "Keep Calamares open for eight hours.", quip: "Hydrate."},
	{id: "on-a-roll", name: "On a roll", area: areaWatching, glyph: [2]string{"", "♨"}, tiers: []int{3, 7, 30, 100}, how: countBest,
		text: "Open Calamares on working days in a row.", quip: "Weekends don't break the streak."},
	{id: "old-friend", name: "Old friend", area: areaWatching, glyph: [2]string{"", "♥"}, tiers: []int{30, 180, 365}, how: countBest,
		text: "Days since you first opened Calamares.", quip: "Thanks for sticking around."},

	{id: "up-to-date", name: "Up to date", area: areaPlatform, glyph: [2]string{"", "⇧"}, tiers: once, medal: tierGold,
		text: "See a shared workflow or action that every caller uses at one version.", quip: "Nobody left behind."},
	{id: "archaeologist", name: "Archaeologist", area: areaPlatform, glyph: [2]string{"", "▤"}, tiers: once, medal: tierBronze,
		text: "Find a caller three major versions behind.", quip: "v1 lives."},
	{id: "deprecation-slayer", name: "Deprecation slayer", area: areaPlatform, glyph: [2]string{"", "†"}, tiers: once, medal: tierGold,
		text: "See Warnings empty.", quip: "The old runtimes have fallen."},
	{id: "runner-whisperer", name: "Runner whisperer", area: areaPlatform, glyph: [2]string{"", "▥"}, tiers: once, medal: tierBronze,
		text: "Open Runners while jobs wait for a runner.", quip: "Somebody has to watch the queue."},
	{id: "not-my-fault", name: "Not my fault", area: areaPlatform, glyph: [2]string{"", "☁"}, tiers: once, medal: tierBronze,
		text: "Open GitHub status during an incident.", quip: "For once, it really wasn't."},

	{id: "hello-world", name: "Hello world", area: areaExplorer, glyph: [2]string{"", "⚑"}, tiers: once, medal: tierBronze,
		text: "Start Calamares.", quip: "Welcome aboard."},
	{id: "tourist", name: "Tourist", area: areaExplorer, glyph: [2]string{"", "⌂"}, tiers: []int{len(allTabNames)}, medal: tierSilver, how: countDistinct,
		text: "Visit every place in the header.", quip: "You've seen it all. Almost."},
	{id: "curious", name: "Curious", area: areaExplorer, glyph: [2]string{"", "ⓘ"}, tiers: []int{10}, medal: tierSilver, how: countDistinct,
		text: "Press i on ten different screens.", quip: "Read the manual. Kind of."},
	{id: "key-master", name: "Key master", area: areaExplorer, glyph: [2]string{"", "⌘"}, tiers: []int{10, 50, 100}, how: countDistinct,
		text: "Use different keys.", quip: "Mouse? Never heard of it."},
	{id: "power-user", name: "Power user", area: areaExplorer, glyph: [2]string{"", "…"}, tiers: []int{10, 50, 250},
		text: "Open the action menu with a dot.", quip: "Everything, one key away."},
	{id: "fashionista", name: "Fashionista", area: areaExplorer, glyph: [2]string{"", "✐"}, tiers: []int{len(themes)}, medal: tierSilver, how: countDistinct,
		text: "Pick every theme.", quip: "Paper suits you."},
	{id: "time-traveller", name: "Time traveller", area: areaExplorer, glyph: [2]string{"", "⟲"}, tiers: once, medal: tierBronze,
		text: "Compare a failed run with its last success.", quip: "What changed? Everything."},

	{id: "konami", name: "Up up down down", area: areaSecret, glyph: [2]string{"", "✚"}, tiers: once, medal: tierGold,
		text: "Enter the Konami code.", quip: "+30 lives.", hint: "Old consoles remember."},
	{id: "fools-gold", name: "Fool's gold", area: areaSecret, glyph: [2]string{"", "☺"}, tiers: once, medal: tierGold,
		text: "Open Calamares on the first of April.", quip: "This badge is real. Probably.", hint: "Check back on a certain day in spring."},
	{id: "clockwork", name: "Clockwork", area: areaSecret, glyph: [2]string{"", "◎"}, tiers: once, medal: tierGold,
		text: "Watch a run pass in exactly its usual time, to the second.", quip: "Swiss made.", hint: "Exactly as long as usual."},
	{id: "living-dangerously", name: "Living dangerously", area: areaSecret, glyph: [2]string{"", "⚠"}, tiers: once, medal: tierSilver,
		text: "Merge a pull request on a Friday after four.", quip: "Bold. Very bold.", hint: "Some days are for merging. Some are not."},
	{id: "insanity", name: "Insanity", area: areaSecret, glyph: [2]string{"", "↺"}, tiers: once, medal: tierSilver,
		text: "Rerun the same run a fifth time.", quip: "And expect a different result.", hint: "Doing the same thing over and over."},
	{id: "ghost", name: "Ghost in the shell", area: areaSecret, glyph: [2]string{"", "◌"}, tiers: []int{25}, medal: tierBronze,
		text: "Dismiss 25 cards with ctrl+x.", quip: "Boo.", hint: "So many cards, gone so fast."},
}

func achievementByID(id string) *achievement {
	for _, x := range achievements {
		if x.id == id {
			return x
		}
	}
	return nil
}

// achProgress is what is kept of your achievements between sessions.
type achProgress struct {
	Counts map[string]int         `json:"counts"`
	Seen   map[string][]string    `json:"seen,omitempty"`
	Earned map[string][]time.Time `json:"earned"` // when each tier was reached, the lowest first
	Daily  map[string]dayCount    `json:"daily,omitempty"`
	First  time.Time              `json:"first"`
	// LastDay is the last working day Calamares was opened on, and Streak
	// how many working days in a row it was opened up to it.
	LastDay string `json:"lastDay,omitempty"`
	Streak  int    `json:"streak,omitempty"`
}

// dayCount counts something on one day.
type dayCount struct {
	Day string `json:"day"`
	N   int    `json:"n"`
}

func newProgress() *achProgress {
	return &achProgress{Counts: map[string]int{}, Seen: map[string][]string{}, Earned: map[string][]time.Time{}, Daily: map[string]dayCount{}}
}

// tierOf is the tier of x you have.
func (p *achProgress) tierOf(x *achievement) tier {
	n := len(p.Earned[x.id])
	switch {
	case n == 0:
		return tierNone
	case x.oneOff():
		return x.medal
	}
	return tier(min(n, len(x.tiers)))
}

// xp is what every tier you have is worth.
func (p *achProgress) xp() int {
	xp := 0
	for _, x := range achievements {
		xp += x.xpUpTo(p.tierOf(x))
	}
	return xp
}

// unlocked counts the achievements you have at least one tier of.
func (p *achProgress) unlocked() int {
	n := 0
	for _, x := range achievements {
		if p.tierOf(x) > tierNone {
			n++
		}
	}
	return n
}

// fraction is how far x is to its next tier, from 0 to 1; 1 once it is at
// its top tier.
func (p *achProgress) fraction(x *achievement) float64 {
	n := p.Counts[x.id]
	need, _ := x.next(n)
	if need == 0 {
		return 1
	}
	return float64(n) / float64(need)
}

// almost says x is worth showing as close: it has started and has a tier left.
func (p *achProgress) almost(x *achievement) bool {
	return !x.secret() && p.Counts[x.id] > 0 && p.tierOf(x) < x.top()
}

// maxLevel is the highest level.
const maxLevel = 15

// levelNeeds is the XP level l needs: each level a little more than the one before.
func levelNeeds(l int) int { return 20 * l * (l - 1) }

func levelOf(xp int) int {
	l := 1
	for l < maxLevel && xp >= levelNeeds(l+1) {
		l++
	}
	return l
}

// levelTitles are the titles of the levels, each from its level on.
var levelTitles = []struct {
	from  int
	title string
}{{1, "Intern"}, {3, "Pipeline apprentice"}, {5, "Build wrangler"}, {7, "Release captain"}, {10, "Workflow sage"}, {13, "Merge maestro"}, {15, "Octolegend"}}

func levelTitle(l int) string {
	t := ""
	for _, lt := range levelTitles {
		if l >= lt.from {
			t = lt.title
		}
	}
	return t
}

// nextTitle is the next title and the level it comes at; "" at the last.
func nextTitle(l int) (string, int) {
	for _, lt := range levelTitles {
		if lt.from > l {
			return lt.title, lt.from
		}
	}
	return "", 0
}

const achievementsFile = "achievements.json"

// loadProgress reads the achievements kept in dir; a missing or broken file
// starts afresh.
func loadProgress(dir string) *achProgress {
	p := newProgress()
	if dir == "" {
		return p
	}
	b, err := os.ReadFile(filepath.Join(dir, achievementsFile))
	if err != nil || json.Unmarshal(b, p) != nil {
		return newProgress()
	}
	if p.Counts == nil {
		p.Counts = map[string]int{}
	}
	if p.Seen == nil {
		p.Seen = map[string][]string{}
	}
	if p.Earned == nil {
		p.Earned = map[string][]time.Time{}
	}
	if p.Daily == nil {
		p.Daily = map[string]dayCount{}
	}
	return p
}

// saveProgress keeps the achievements next to the config.
func (a *App) saveProgress() {
	dir := a.cfg.Dir()
	if dir == "" {
		return
	}
	b, err := json.Marshal(a.ach)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.note(nError, "Could not save your achievements: "+err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(dir, achievementsFile), b, 0o644); err != nil {
		a.note(nError, "Could not save your achievements: "+err.Error())
	}
}

// earn adds n to the counter of achievement id.
func (a *App) earn(id string, n int) { a.count(id, func(c int) int { return c + n }) }

// earnOnce counts a one-off as done.
func (a *App) earnOnce(id string) { a.earnBest(id, 1) }

// earnBest keeps v as the counter of id when it is higher.
func (a *App) earnBest(id string, v int) { a.count(id, func(c int) int { return max(c, v) }) }

// earnSeen counts value for id once, however often it comes back.
func (a *App) earnSeen(id, value string) {
	if slices.Contains(a.ach.Seen[id], value) {
		return
	}
	a.ach.Seen[id] = append(a.ach.Seen[id], value)
	a.count(id, func(int) int { return len(a.ach.Seen[id]) })
}

// count moves the counter of id and unlocks the tiers it reaches.
func (a *App) count(id string, move func(int) int) {
	x := achievementByID(id)
	if x == nil {
		return
	}
	before := a.ach.Counts[id]
	after := move(before)
	if after == before {
		return
	}
	a.ach.Counts[id] = after
	a.unlock(x, after)
	a.saveProgress()
}

// unlock records the tiers of x that count n reaches, and shows a card for
// the highest of them, and one for a new level.
func (a *App) unlock(x *achievement, n int) {
	had, want := a.ach.tierOf(x), x.tierAt(n)
	if want <= had {
		return
	}
	level := levelOf(a.ach.xp())
	now := time.Now()
	if x.oneOff() {
		a.ach.Earned[x.id] = []time.Time{now}
	} else {
		for t := had + 1; t <= want; t++ {
			a.ach.Earned[x.id] = append(a.ach.Earned[x.id], now)
		}
	}
	if !a.cfg.AchievementsOn() {
		return
	}
	a.addAward(&award{x: x, tier: want})
	if l := levelOf(a.ach.xp()); l > level {
		a.addAward(&award{level: l})
	}
}

// earnOnStart counts what opening Calamares counts: the first time, the
// hour, the day and the streak of working days. It runs again when the day
// changes while Calamares is open.
func (a *App) earnOnStart(now time.Time) {
	fresh := a.ach.First.IsZero()
	if fresh {
		a.ach.First = now
	}
	day := now.Format(time.DateOnly)
	if workingDay(now) && a.ach.LastDay != day {
		if a.ach.LastDay == previousWorkingDay(now).Format(time.DateOnly) {
			a.ach.Streak++
		} else {
			a.ach.Streak = 1
		}
		a.ach.LastDay = day
		a.saveProgress()
	} else if fresh {
		a.saveProgress()
	}
	a.earnOnce("hello-world")
	if h := now.Hour(); h >= 5 && h < 7 {
		a.earnOnce("early-bird")
	}
	if now.Month() == time.April && now.Day() == 1 {
		a.earnOnce("fools-gold")
	}
	a.earnBest("on-a-roll", a.ach.Streak)
	a.earnBest("old-friend", int(now.Sub(a.ach.First).Hours()/24))
}

func workingDay(t time.Time) bool { return t.Weekday() != time.Saturday && t.Weekday() != time.Sunday }

// previousWorkingDay is the working day before t.
func previousWorkingDay(t time.Time) time.Time {
	d := t.AddDate(0, 0, -1)
	for !workingDay(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// earnOnTick counts what time counts while Calamares stays open.
func (a *App) earnOnTick(now time.Time) {
	if now.Sub(a.startedAt) >= 8*time.Hour {
		a.earnOnce("marathon")
	}
	if workingDay(now) && a.ach.LastDay != now.Format(time.DateOnly) {
		a.earnOnStart(now)
	}
}

// earnMerge counts a merged pull request; d is what we know of it, when we do.
func (a *App) earnMerge(now time.Time, d *gh.PullRequestDetail) {
	a.earn("merge-master", 1)
	day := now.Format(time.DateOnly)
	merges := a.ach.Daily["merges"]
	if merges.Day != day {
		merges = dayCount{Day: day}
	}
	merges.N++
	a.ach.Daily["merges"] = merges
	a.earnBest("merge-train", merges.N)
	if !workingDay(now) {
		a.earnOnce("weekend-warrior")
	}
	if now.Weekday() == time.Friday && now.Hour() >= 16 {
		a.earnOnce("living-dangerously")
	}
	if d == nil {
		return
	}
	changed := d.Additions + d.Deletions
	if changed > 1000 {
		a.earnOnce("big-bang")
	}
	if changed > 0 && changed <= 2 && d.Files == 1 {
		a.earnOnce("one-liner")
	}
	if d.Deletions-d.Additions >= 100 {
		a.earnOnce("spring-cleaning")
	}
}

// earnReview counts a review by what it said.
func (a *App) earnReview(event string, n int) {
	switch event {
	case "APPROVE":
		a.earn("rubber-stamp", n)
	case "REQUEST_CHANGES":
		a.earn("nitpicker", n)
	case "COMMENT":
		a.earn("second-opinion", n)
	}
}

// earnRerun counts asking for a run to run again.
func (a *App) earnRerun(run gh.Run, debug bool) {
	if debug {
		a.earnOnce("debug-mode")
	}
	if run.RunAttempt >= 5 {
		a.earnOnce("insanity")
	}
}

// earnFinished counts a run you watched to its end.
func (a *App) earnFinished(w *watch, now time.Time) {
	run := w.run
	a.earn("pipeline-whisperer", 1)
	took := run.Duration(now)
	if took >= time.Hour {
		a.earnOnce("slow-cooker")
	}
	if now.Hour() < 5 {
		a.earnOnce("night-owl")
	}
	if StatusKind(run.Status, run.Conclusion) != KOK {
		return
	}
	a.earnOnce("first-light")
	if w.after > 0 {
		a.earn("second-wind", 1)
	}
	if run.RunAttempt >= 3 {
		a.earn("flake-hunter", 1)
	}
	if usual := medianDuration(a.comparable(w.repo, run, a.workflowRuns(w.repo, run.Name))); usual > 0 {
		if took < usual*7/10 {
			a.earnOnce("speed-demon")
		}
		if took.Round(time.Second) == usual.Round(time.Second) {
			a.earnOnce("clockwork")
		}
	}
	if jobs, ok := a.client.CachedJobs(w.repo, run.ID); ok && len(jobs) >= 20 {
		a.earnOnce("no-spoon")
	}
	if a.backToGreen(w.repo, run, w.after > 0) {
		a.earn("green-again", 1)
	}
}

// backToGreen says a run that passed on its repo's default branch follows a
// failure there: an earlier attempt of the same run, or the run of its
// workflow before it.
func (a *App) backToGreen(repo string, run gh.Run, rerun bool) bool {
	if run.HeadBranch == "" || !strings.EqualFold(run.HeadBranch, a.defaultBranch(repo)) {
		return false
	}
	if rerun {
		return true
	}
	for _, r := range a.workflowRuns(repo, run.Name) {
		if r.ID == run.ID || r.HeadBranch != run.HeadBranch || r.Active() || !r.CreatedAt.Before(run.CreatedAt) {
			continue
		}
		return StatusKind(r.Status, r.Conclusion) == KFail
	}
	return false
}

// screenName tells screens apart, and the views of one screen.
func screenName(v View) string {
	name := fmt.Sprintf("%T", v)
	if n, ok := v.(viewNamer); ok {
		name += " › " + n.ViewName()
	}
	return name
}

// konamiCode is the keys of the Konami code, as Bubble Tea names them.
var konamiCode = []string{"up", "up", "down", "down", "left", "right", "left", "right", "b", "a"}

// earnKey counts a key: the keys you use, and the Konami code.
func (a *App) earnKey(key string) {
	a.keyTrail = append(a.keyTrail, key)
	if len(a.keyTrail) > len(konamiCode) {
		a.keyTrail = a.keyTrail[len(a.keyTrail)-len(konamiCode):]
	}
	if slices.Equal(a.keyTrail, konamiCode) {
		a.earnOnce("konami")
	}
}

// observe counts what the screen on display shows: an empty list of
// failures or warnings, jobs waiting for a runner, an incident, and how
// many runs you watch at once.
func (a *App) observe() {
	a.earnBest("watch-party", a.runningWatches())
	switch v := a.top().(type) {
	case *statusView:
		if len(a.openIncidents()) > 0 {
			a.earnOnce("not-my-fault")
		}
	case *activityView:
		a.observePlace(v)
	}
}

func (a *App) observePlace(v *activityView) {
	switch v.filter {
	case viewFailing:
		if a.st.loaded && len(v.repos(a)) > 0 && v.failing.count(a) == 0 {
			a.earnOnce("clean-sweep")
		}
	case viewWarnings:
		if v.read[viewWarnings] && !v.warnings.busy(a) && v.warnings.count(a) == 0 {
			a.earnOnce("deprecation-slayer")
		}
	case viewRunners:
		if len(v.runners.waits) > 0 {
			a.earnOnce("runner-whisperer")
		}
	case viewTime:
		for _, r := range v.time.rows {
			if r.prior > 0 && r.week >= r.prior*3/2 {
				a.earnSeen("time-lord", r.key)
			}
		}
	case viewSharedWorkflows:
		a.observeShared(v.shared)
	case viewSharedActions:
		a.observeShared(v.sharedActions)
	}
}

// observeShared counts a shared item every caller uses at one tag, and a
// caller three major versions behind.
func (a *App) observeShared(s *sharedView) {
	for _, it := range s.items {
		if len(it.versions) == 1 && len(it.versions[0].users) >= 2 {
			if _, tag := majorOf(it.versions[0].version); tag {
				a.earnOnce("up-to-date")
			}
		}
		for _, ver := range it.versions {
			have, ok1 := majorOf(ver.version)
			newest, ok2 := majorOf(ver.ahead)
			if ver.behind && ok1 && ok2 && newest-have >= 3 {
				a.earnOnce("archaeologist")
			}
		}
	}
}

// majorOf is the major version of a tag such as v3 or v3.1.0.
func majorOf(version string) (int, bool) {
	s := strings.TrimPrefix(strings.ToLower(version), "v")
	end := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if end == 0 || s == "" {
		return 0, false
	}
	if end > 0 {
		s = s[:end]
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// levelText is your level as the pill in the header says it.
func (a *App) levelText() string {
	return fmt.Sprintf("Lv %d", levelOf(a.ach.xp()))
}
