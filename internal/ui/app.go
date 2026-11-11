// Package ui is the Calamares terminal interface.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vvanouytsel/calamares/internal/config"
	"github.com/vvanouytsel/calamares/internal/gh"
)

// View is one screen on the navigation stack.
type View interface {
	ID() int
	Init(a *App) tea.Cmd
	Update(a *App, msg tea.Msg) tea.Cmd
	Render(a *App, w, h int) string
	Crumb() string
	// Keys are the keys of this screen as it is now, most important first.
	Keys(a *App) []Binding
	// Refresh reloads data when it's due (or always when force is set).
	Refresh(a *App, force bool) tea.Cmd
}

// Container is a view made of other views (e.g. the repo screen's runs panes);
// messages addressed to a child are routed through it.
type Container interface{ Children() []View }

// syncer is implemented by screens whose list is made from data that changes
// behind them: the store, the clock, the saved filter. sync brings the list
// up to date. The app calls it before a key reaches the screen and before
// drawing it, so what a key acts on never depends on the screen having been
// drawn first.
type syncer interface{ sync(a *App) }

// syncTop brings the list of the screen on display up to date, unless
// nothing that could change it happened since it last did.
func (a *App) syncTop() {
	if a.listsFresh && a.listsOf == a.top() {
		return
	}
	if s, ok := a.top().(syncer); ok {
		s.sync(a)
	}
	a.listsFresh, a.listsOf = true, a.top()
}

// movesOnly says msg only moves the cursor or the animation along: the lists
// stay as they are. Rebuilding a long list on every key of a held arrow is
// what made moving through it lag.
func (a *App) movesOnly(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "down", "j", "k", "pgup", "pgdown", "home", "end":
			return a.modal == nil && a.mode != modeSearch
		}
	case animMsg:
		return time.Since(a.frameAt) < frameEvery
	}
	return false
}

// Filterer is implemented by views whose list "/" filters.
type Filterer interface {
	SetFilter(a *App, q string)
	FilterValue() string
	FilterInfo(a *App) filterInfo
}

// Freshness is implemented by screens that load data: when they last did,
// and whether they are loading now. A screen is refreshed when it is shown
// with data older than staleAfter.
type Freshness interface {
	Updated(a *App) time.Time
	Loading(a *App) bool
}

// staleAfter is how old a screen's data may be before showing it checks GitHub.
const staleAfter = 5 * time.Minute

// RepoScoped is implemented by views that belong to a repository.
type RepoScoped interface{ RepoName() string }

type targeted interface{ viewID() int }

type mode int

const (
	modeNormal mode = iota
	modeSearch
)

// The top-level places.
const (
	tabRepos = iota
	tabActions
	tabPulls
	tabWatching
	tabPlatform
	tabIssues
	tabAchievements
)

// allTabNames are the places, in the order of their digits; a new place is
// added here and to allTabIcons, and the header, the digits, < > and the
// go-to box pick it up.
var (
	allTabNames = []string{"Repos", "Actions", "Pull requests", "Watching", "Platform", "Issues", "Achievements"}
	allTabIcons = []string{"repo", "actions", "pr", "watching", "platform", "issue", "trophy"}
)

// tabNames and tabIcons are the places on display: all of them, but for
// Achievements when the config switches it off.
var (
	tabNames = allTabNames
	tabIcons = allTabIcons
)

// stepTab moves to the previous or next place, wrapping around.
func (a *App) stepTab(d int) tea.Cmd {
	n := len(tabNames)
	return a.setTab((a.tab + d + n) % n)
}

// tabDigits are the keys of the places: 1, 2, 3 …
func tabDigits() []string {
	var out []string
	for i := range tabNames {
		out = append(out, string(rune('1'+i)))
	}
	return out
}

// Options configure the app at start-up.
type Options struct {
	Version   string
	ReadOnly  bool
	StartRepo string
	Branch    string
	FromCwd   bool
	// Theme and Animations override the config for this run; "" keeps it.
	Theme      string
	Animations string
	// SaveCache persists the response cache; it runs every few minutes, off
	// the UI goroutine, so a crash doesn't turn the next start into a cold one.
	SaveCache func()
}

// saveCacheEvery is how often the response cache is written to disk.
const saveCacheEvery = 5 * time.Minute

// App is the root Bubble Tea model.
type App struct {
	// listsFresh says the list of listsOf is up to date; see syncTop.
	listsFresh bool
	listsOf    View
	keyAt      time.Time // when the last key came
	// reads holds how much goes on in each repo; see readInfoOf.
	reads readCache
	// people are the people whose page you opened last, newest first.
	people []string

	client  *gh.Client
	cfg     *config.Config
	opts    Options
	me      string
	w, h    int
	tab     int
	roots   []View
	stack   []View
	nextID  int
	frame   int
	frameAt time.Time // when frame last advanced
	moving  bool      // something drawn last moves with the animation
	// A burst redraws at 120 frames a second while a glow plays; see glow.go.
	bursting   bool
	burstUntil time.Time
	glowing    bool
	flowing    bool // light runs along the lines of a pipeline on display
	themeName  string
	fade       *themeFade // a change of theme in progress
	modalAt    int64      // when the dialog on display opened
	modalOpen  bool
	ticks      int
	mode       mode
	input      textinput.Model
	pal        palette
	modal      Modal
	flash      string
	flashKind  noticeKind
	flashAt    time.Time
	// toasts are the cards above the key bar for what happens to the runs you
	// watch: started, passed, failed.
	toasts []*toast
	focus  focusState // whether the terminal has the focus, when it says
	// notices is everything the key bar said this session; unreadErrors
	// counts the errors among them not yet seen on the Messages screen.
	notices      []notice
	unreadErrors int
	// windowTitle is what the terminal's title was last set to.
	windowTitle string
	// noAnswer counts the requests in a row GitHub did not answer in time,
	// and serverTrouble those it failed on its side (HTTP 500 or more), each
	// told as one message that counts up instead of one per request.
	noAnswer, serverTrouble countedNote
	// actNeed and actRepo are the capability of the key being pressed, so the
	// action it starts files a refusal under it.
	actNeed gh.Capability
	actRepo string

	st         *store
	repos      []gh.Repo
	ownerOf    []gh.Repo // the repos ownerName was worked out from
	ownerName  string
	twoLine    bool // lists show each title on a line of its own
	compact    bool // lists of runs are a table of one line per run, not cards of two
	filterOff  bool // the saved run filter is switched off for this session
	runQuery   query
	runQueryOf string // the filter and login runQuery was parsed from
	workflows  map[string][]gh.Workflow
	typicals   map[typicalKey]gh.Baseline
	alerts     map[string]*alertState // Dependabot alerts per repo, read for the one under the cursor
	// alertsBusy says the alerts of the listed repos are being read.
	alertsBusy bool
	users      map[string][]string // who can be assigned, per repo
	pending    map[string][]gh.DraftComment
	logs       map[int64]*jobLog
	watches    map[int64]*watch
	// repoWatches is how the watch of each repo is doing, by lower-case
	// name; unwatched are the runs they found that you stopped watching.
	repoWatches map[string]*repoWatchState
	unwatched   map[int64]bool
	// prWatches are the pull requests you watch, by prWatchKey.
	prWatches map[string]*prWatch
	// aside holds what you set aside with d, and when; see setaside.go.
	aside map[string]time.Time
	// live keeps the screen on display up to date while you're here.
	live bool
	// frozen is the screen as it was when ctrl+s froze it, so a selection in
	// the terminal survives; "" while the screen keeps updating.
	frozen   string
	freezing bool // the screen is being drawn to be frozen

	savedAt     time.Time
	search      repoSearch
	listSearch  bool   // the search box was opened with "/" and filters the list
	savedFilter string // the list's filter before the search box opened
	suggest     suggestState

	// ach is your achievements; awards the cards for what you unlocked, the
	// first on display. startedAt is when this session began and keyTrail
	// the last keys pressed.
	ach       *achProgress
	awards    []*award
	startedAt time.Time
	keyTrail  []string
}

// New creates the app.
func New(client *gh.Client, cfg *config.Config, opts Options) *App {
	cfg.NormalizeIcons()
	ascii = cfg.Icons == "ascii"
	themeName := cfg.Theme
	if opts.Theme != "" {
		themeName = opts.Theme
	}
	th := themeNamed(themeName)
	if th == nil {
		th = &themes[0]
	}
	applyTheme(*th)
	level := cfg.Animations
	if opts.Animations != "" {
		level = opts.Animations
	}
	if level == "" {
		level = os.Getenv("CALAMARES_ANIMATIONS")
	}
	animLevel = parseAnim(level)
	baseOn = !colourOff() && !ascii
	nerd = cfg.Icons == "nerd"
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 200
	ti.Cursor.Style = sCyan
	ti.TextStyle = sWhite
	tabNames, tabIcons = allTabNames, allTabIcons
	if !cfg.AchievementsOn() {
		tabNames, tabIcons = allTabNames[:tabAchievements], allTabIcons[:tabAchievements]
	}
	a := &App{
		themeName: th.Name,
		compact:   true,
		client:    client,
		cfg:       cfg,
		opts:      opts,
		input:     ti,
		st:        newStore(),
		workflows: map[string][]gh.Workflow{},
		typicals:  map[typicalKey]gh.Baseline{},
		users:     map[string][]string{},
		alerts:    map[string]*alertState{},
		pending:   map[string][]gh.DraftComment{},
		logs:      map[int64]*jobLog{},
		watches:   map[int64]*watch{},
		savedAt:   time.Now(),
		startedAt: time.Now(),
		ach:       loadProgress(cfg.Dir()),

		repoWatches: map[string]*repoWatchState{},
		unwatched:   map[int64]bool{},
		prWatches:   map[string]*prWatch{},
	}
	a.roots = []View{newReposView(a), newActivityView(a), newPullsView(a, ""), newWatchView(a), newPlatformView(a), newIssuesView(a, ""), newAchievementsView(a)}
	a.tab = tabRepos
	a.stack = []View{a.roots[tabRepos]}
	if opts.StartRepo != "" {
		a.tab = tabRepos
		rv := newRepoView(a, opts.StartRepo, opts.Branch)
		rv.focus = paneRuns
		a.stack = []View{a.roots[tabRepos], rv}
	}
	return a
}

func (a *App) newID() int { a.nextID++; return a.nextID }

type tickMsg time.Time
type animMsg time.Time

// frameEvery is how often the frame of spinners and running statuses moves on.
const frameEvery = 230 * time.Millisecond

// heldKeyGap is how close keys come when one is held down.
const heldKeyGap = 120 * time.Millisecond

type userMsg struct {
	user gh.User
	err  error
}
type flashMsg struct {
	kind noticeKind
	text string
}
type actionMsg struct {
	viewID int // the screen the action was taken on
	repo   string
	text   string
	err    error
	then   func() tea.Cmd // what to do once it succeeded, if anything
}

// refreshNowMsg refreshes, after an action, the screen it was taken on (if
// it is still open), reading only what the action in repo may have changed.
type refreshNowMsg struct {
	viewID int
	repo   string
}

// repoRefresher is implemented by screens covering many repos, so an action
// in one repo re-reads only that one.
type repoRefresher interface {
	RefreshRepo(a *App, repo string) tea.Cmd
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// anim is the next beat of the animation: quick while something in flight
// is drawn, slow while only a spinner turns.
func (a *App) anim() tea.Cmd {
	d := 250 * time.Millisecond
	switch {
	case a.flowing && animLevel >= animFull:
		d = 16 * time.Millisecond // light running along lines needs a smooth 60 frames a second
	case a.moving && animLevel >= animSubtle:
		d = 70 * time.Millisecond
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return animMsg(t) })
}

// Init seeds shared data from the cache and checks the screen on display
// with GitHub; other screens load when you open them.
func (a *App) Init() tea.Cmd {
	a.st.seed(a)
	a.loadWatches()
	a.loadPRWatches()
	a.loadRepoActivity()
	a.loadAside()
	a.earnOnStart(time.Now())
	a.earnSeen("tourist", tabNames[a.tab])
	cmds := []tea.Cmd{tick(), a.anim(), a.loadUser(), a.syncRate(), a.pollWatches(), a.pollRepoWatches(), a.pollPRWatches(), a.pollActivity(), a.keysNews()}
	if len(a.stack) == 1 {
		cmds = append(cmds, a.visit())
	}
	for _, v := range a.stack[1:] {
		cmds = append(cmds, v.Init(a))
	}
	if a.opts.StartRepo != "" && a.opts.FromCwd {
		msg := "Opened " + a.opts.StartRepo + " from this folder"
		if a.opts.Branch != "" {
			msg += " · branch " + a.opts.Branch
		}
		cmds = append(cmds, a.flashCmd(nInfo, msg))
	}
	return tea.Batch(cmds...)
}

func (a *App) loadUser() tea.Cmd {
	return func() tea.Msg {
		u, err := a.client.CurrentUser(context.Background())
		return userMsg{u, err}
	}
}

func (a *App) syncRate() tea.Cmd {
	return func() tea.Msg {
		_ = a.client.SyncRate(context.Background())
		return nil
	}
}

func (a *App) top() View { return a.stack[len(a.stack)-1] }

func (a *App) push(v View) tea.Cmd {
	a.stack = append(a.stack, v)
	return v.Init(a)
}

func (a *App) pop() tea.Cmd {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
	}
	return a.visit()
}

// setTab switches to a top-level place, leaving whatever was open.
func (a *App) setTab(t int) tea.Cmd {
	a.tab = t
	a.earnSeen("tourist", tabNames[t])
	a.stack = []View{a.roots[t]}
	return a.visit()
}

// staleRefresher is implemented by screens that can read only their old
// parts, such as the Actions tab with its many repos.
type staleRefresher interface {
	RefreshStale(a *App) tea.Cmd
}

// visit refreshes the screen on display if its data is old.
func (a *App) visit() tea.Cmd {
	f, ok := a.top().(Freshness)
	if !ok || f.Loading(a) || time.Since(f.Updated(a)) < staleAfter {
		return nil
	}
	if s, ok := a.top().(staleRefresher); ok {
		return s.RefreshStale(a)
	}
	return a.top().Refresh(a, true)
}

// Scoped is implemented by screens whose refresh reads only part of their
// list while a filter is set.
type Scoped interface{ RefreshScope(a *App) string }

func (a *App) refreshScope() string {
	if s, ok := a.top().(Scoped); ok {
		return s.RefreshScope(a)
	}
	return ""
}

// Follower is implemented by screens that show work still in progress; they
// keep themselves up to date while you're here, whatever the live setting.
type Follower interface{ Following() bool }

func (a *App) following() bool {
	f, ok := a.top().(Follower)
	return ok && f.Following()
}

// KeepsUp is implemented by screens that always keep themselves up to date,
// whatever the live setting, because what they read is cheap enough.
type KeepsUp interface{ KeepsUp() bool }

func (a *App) keepsUp() bool {
	k, ok := a.top().(KeepsUp)
	return ok && k.KeepsUp()
}

// liveNow reports whether the screen on display updates itself right now:
// live updates are on, or it follows a running job.
func (a *App) liveNow() bool { return a.live || a.following() || a.keepsUp() }

// openRepo shows a repository (under the Repos tab).
func (a *App) openRepo(repo, branch string, wfID int64) tea.Cmd {
	a.tab = tabRepos
	a.stack = []View{a.roots[tabRepos]}
	v := newRepoView(a, repo, branch)
	v.wantWF = wfID
	if branch != "" || wfID != 0 {
		v.focus = paneRuns
	}
	return a.push(v)
}

func (a *App) flashCmd(kind noticeKind, text string) tea.Cmd {
	return func() tea.Msg { return flashMsg{kind, text} }
}

// setFlash says something in the key bar and keeps it for the Messages screen.
// The chip carries the check or the cross, so a leading one is dropped.
func (a *App) setFlash(kind noticeKind, text string) {
	text = strings.TrimPrefix(strings.TrimPrefix(text, "✓ "), "✗ ")
	switch {
	case kind == nError && strings.Contains(text, gh.ErrNoAnswer.Error()):
		text = a.noteCounted(&a.noAnswer, gh.ErrNoAnswer.Error(), "timed out")
	case kind == nError && strings.Contains(text, gh.ErrServerTrouble.Error()):
		how := "failed"
		// keep how GitHub answered: "(HTTP 502 Bad Gateway, tried 3 times)"
		if _, rest, ok := strings.Cut(text, gh.ErrServerTrouble.Error()+" ("); ok {
			if status, _, ok := strings.Cut(rest, ")"); ok {
				how += " (" + status + ")"
			}
		}
		text = a.noteCounted(&a.serverTrouble, gh.ErrServerTrouble.Error(), how)
	default:
		a.note(kind, text)
	}
	a.flash, a.flashKind, a.flashAt = text, kind, time.Now()
}

// act runs an action that changes something in repo, then refreshes it.
// Read-only mode refuses it.
func (a *App) act(repo, done string, fn func(ctx context.Context) error) tea.Cmd {
	return a.actThen(repo, done, fn, nil)
}

// actThen is act with something to do once the action succeeded.
func (a *App) actThen(repo, done string, fn func(ctx context.Context) error, then func() tea.Cmd) tea.Cmd {
	if a.opts.ReadOnly {
		return a.flashCmd(nHint, readOnlyText)
	}
	viewID := a.top().ID()
	need, capRepo := a.actNeed, a.actRepo
	if capRepo == "" {
		capRepo = repo
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if need != gh.CapRead {
			ctx = gh.WithCapability(ctx, capRepo, need)
		}
		return actionMsg{viewID: viewID, repo: repo, text: done, err: fn(ctx), then: then}
	}
}

// refreshAfterActionAt is when a screen is refreshed after an action: a
// new run or a state change can take a few seconds to show up on GitHub.
var refreshAfterActionAt = []time.Duration{2 * time.Second, 10 * time.Second}

func (a *App) refreshAfterAction(viewID int, repo string) tea.Cmd {
	var cmds []tea.Cmd
	for _, d := range refreshAfterActionAt {
		cmds = append(cmds, tea.Tick(d, func(time.Time) tea.Msg { return refreshNowMsg{viewID, repo} }))
	}
	return tea.Batch(cmds...)
}

// refreshAfter re-reads what an action may have changed, on the screen it
// was taken on, if that screen is still on display.
func (a *App) refreshAfter(m refreshNowMsg) tea.Cmd {
	v := findView(a.top(), m.viewID)
	if v == nil {
		return nil
	}
	if r, ok := v.(repoRefresher); ok {
		return r.RefreshRepo(a, m.repo)
	}
	return v.Refresh(a, true)
}

func findView(v View, id int) View {
	if v.ID() == id {
		return v
	}
	if c, ok := v.(Container); ok {
		for _, ch := range c.Children() {
			if f := findView(ch, id); f != nil {
				return f
			}
		}
	}
	return nil
}

// Update handles every message.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(burstMsg); ok {
		return a, a.onBurst()
	}
	moves := a.movesOnly(msg)
	if !moves {
		a.listsFresh = false
	}
	m, cmd := a.update(msg)
	if !moves {
		a.listsFresh = false
		a.observe()
	}
	switch msg.(type) {
	case tea.KeyMsg:
		// A held key comes faster than a glow can play; drawing every frame
		// of it would only make the keys queue up.
		held := time.Since(a.keyAt) < heldKeyGap
		a.keyAt = time.Now()
		if !held {
			if k := a.kick(); k != nil {
				cmd = tea.Batch(cmd, k)
			}
		}
	}
	if t := a.titleCmd(); t != nil {
		cmd = tea.Batch(cmd, t)
	}
	return m, cmd
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, ok := a.st.handle(a, msg); ok {
		return a, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.frozen = ""
		return a, nil

	case tickMsg:
		a.ticks++
		if a.flash != "" && time.Since(a.flashAt) > a.flashKind.lifetime() {
			a.flash = ""
		}
		a.expireToasts()
		a.expireAwards()
		if a.ticks%60 == 0 {
			a.earnOnTick(time.Now())
		}
		cmds := []tea.Cmd{tick()}
		if a.liveNow() {
			cmds = append(cmds, a.top().Refresh(a, false))
		}
		if a.ticks%int(watchEvery/time.Second) == 0 {
			cmds = append(cmds, a.pollWatches())
		}
		if a.ticks%int(repoWatchEvery/time.Second) == 0 {
			cmds = append(cmds, a.pollRepoWatches(), a.pollActivity())
		}
		if a.ticks%int(prWatchEvery/time.Second) == 0 {
			cmds = append(cmds, a.pollPRWatches())
		}
		if a.st.pushesState.waited(pushesEvery) {
			cmds = append(cmds, a.st.loadPushes(a))
		}
		cmds = append(cmds, a.wantAlerts(), a.pollStatus())
		if save := a.opts.SaveCache; save != nil && time.Since(a.savedAt) > saveCacheEvery {
			a.savedAt = time.Now()
			cmds = append(cmds, func() tea.Msg { save(); return nil })
		}
		return a, tea.Batch(cmds...)

	case animMsg:
		if time.Since(a.frameAt) >= frameEvery {
			a.frame++
			a.frameAt = time.Now()
		}
		return a, a.anim()

	case refreshNowMsg:
		return a, a.refreshAfter(msg)

	case searchTickMsg:
		return a, a.runSearch(msg)

	case searchResultMsg:
		a.searchDone(msg)
		return a, nil

	case userMsg:
		if msg.err != nil {
			a.setFlash(nError, "Could not load your user: "+msg.err.Error())
		} else {
			a.me = msg.user.Login
		}
		return a, nil

	case flashMsg:
		a.setFlash(msg.kind, msg.text)
		return a, nil

	case actionMsg:
		if msg.err != nil {
			if d, ok := gh.AsDenial(msg.err); ok {
				a.setFlash(nHint, lockMark()+d.Reason+" · "+d.Fix)
				return a, nil
			}
			a.setFlash(nError, msg.err.Error())
			return a, nil
		}
		if msg.text != "" {
			a.setFlash(nDone, msg.text)
		}
		var then tea.Cmd
		if msg.then != nil {
			then = msg.then()
		}
		return a, tea.Batch(a.refreshAfterAction(msg.viewID, msg.repo), then)

	case watchPolledMsg:
		return a, a.handleWatchPolled(msg)

	case repoWatchPolledMsg:
		return a, a.handleRepoWatchPolled(msg)

	case prPulseMsg:
		return a, a.handlePRPulse(msg)

	case repoActivityMsg:
		return a, a.handleRepoActivity(msg)

	case finishedMsg:
		a.gotFinished(msg)
		return a, nil

	case dispatchedPollMsg:
		return a, a.findDispatched(msg)

	case dispatchedFoundMsg:
		return a, a.foundDispatched(msg)

	case tea.FocusMsg:
		a.focus = focusYes
		return a, nil

	case tea.BlurMsg:
		a.focus = focusNo
		return a, nil

	case modalMsg:
		a.modal = msg.m
		return a, nil

	case manyAlertsMsg:
		a.gotManyAlerts(msg)
		return a, nil
	case alertsMsg:
		a.gotAlerts(msg)
		return a, nil

	case downloadMsg:
		return a, a.handleDownload(msg)

	case formAsyncMsg:
		msg.apply()
		return a, nil

	case pickerItemsMsg:
		if p, ok := a.modal.(*pickerModal); ok && p.key == msg.key {
			p.add(msg.items)
		}
		return a, nil

	case tea.KeyMsg:
		return a, a.handleKey(msg)

	case targeted:
		for _, v := range append(append([]View{}, a.stack...), a.roots...) {
			if f := findView(v, msg.viewID()); f != nil {
				return a, f.Update(a, msg)
			}
		}
		return a, nil
	}
	return a, a.top().Update(a, msg)
}

func (a *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	a.earnKey(key)
	if a.frozen != "" {
		a.frozen = ""
		if key == "ctrl+s" {
			return nil
		}
	}
	if a.modal != nil {
		done, cmd := a.modal.Update(a, msg)
		if done {
			a.modal = nil
		}
		return cmd
	}
	if a.mode == modeSearch {
		return a.updateSearch(msg)
	}

	a.syncTop()
	if cmd, ok := a.press(a.globalKeys(a.top()), key); ok {
		return cmd
	}
	return a.top().Update(a, msg)
}

// View renders the whole screen: top bar, (search box), main area, key bar.
func (a *App) View() string {
	if a.frozen != "" {
		return a.frozen
	}
	if a.w < 50 || a.h < 12 {
		return "Calamares needs a terminal of at least 50×12."
	}
	smooth, glowing, flowing = false, false, false
	a.stepFade()
	parts := []string{a.renderTopBar()}
	h := a.h - 1 - topBarHeight
	if a.mode == modeSearch {
		prompt := a.renderPrompt()
		parts = append(parts, prompt)
		h -= strings.Count(prompt, "\n") + 1
	}
	a.syncTop()
	if l, ok := a.top().(located); ok && l.where().inRun() {
		parts = append(parts, a.contextLine(l.where(), a.w))
		h--
	}
	main := a.top().Render(a, a.w, h)
	if a.mode == modeSearch && !a.listSearch {
		main = splice(strings.Split(main, "\n"), strings.Split(a.pal.render(a, min(a.w-2, 96)), "\n"), 1, 0)
	}
	if a.modal == nil && a.mode != modeSearch {
		main = a.laid(main, a.w, h)
	}
	parts = append(parts, main, a.renderKeyBar())
	out := strings.Join(parts, "\n")
	if a.modal != nil {
		out = overlay(out, a.modalBox(), a.w, a.h)
	}
	a.noteModal()
	out = paintBase(out, a.w, a.h)
	a.moving, a.glowing, a.flowing = smooth, glowing, flowing
	return out
}

func gradient(s string, from, to [3]int) string {
	rs := []rune(s)
	var b strings.Builder
	n := max(len(rs)-1, 1)
	for i, r := range rs {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		c := [3]int{}
		for k := 0; k < 3; k++ {
			c[k] = from[k] + (to[k]-from[k])*i/n
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2]))).Render(string(r)))
	}
	return b.String()
}

// freshness says how old the data on screen is.
func (a *App) freshness() string {
	f, ok := a.top().(Freshness)
	switch {
	case !ok:
		return ""
	case f.Loading(a):
		return a.refreshing()
	case f.Updated(a).IsZero():
		return sDim.Render("not loaded")
	}
	ago := time.Since(f.Updated(a))
	text := "updated " + age(f.Updated(a), time.Now()) + " ago"
	if ago < 10*time.Second {
		text = "updated just now"
	}
	if ago > staleAfter {
		return sAmber.Render(text)
	}
	return sDim.Render(text)
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// refreshing is the animated note that the screen is reading from GitHub, and
// what part of it.
func (a *App) refreshing() string {
	text := spin(spinner, a.frame) + " refreshing"
	if scope := a.refreshScope(); scope != "" {
		text += " " + scope
	}
	return sCyan.Bold(true).Render(text + "…")
}

// freeze stops the screen from changing, as a terminal drops a selection
// when a line in it is redrawn. Data keeps loading underneath.
func (a *App) freeze() {
	a.freezing = true
	a.frozen = a.View()
	a.freezing = false
}

// liveState says whether the screen keeps itself up to date.
func (a *App) liveState() string {
	switch {
	case a.freezing:
		return sCyan.Bold(true).Render("❄ frozen · ctrl+s resumes")
	case a.live || a.keepsUp():
		return pill("● live", cGreen, cOnAccent)
	case a.following():
		return sGreen.Render("● live while it runs")
	}
	return sDim.Render("○ live off ") + keycap("L")
}

// budget says when the API requests left this hour run low: in red when
// Calamares has paused reading to stay under the limit. While there is
// plenty left it says nothing.
func (a *App) budget() string {
	rate, now := a.client.Rate(), time.Now()
	switch {
	case now.Before(rate.PausedUntil):
		return sRed.Render("GitHub asked to slow down · paused until " + rate.PausedUntil.Local().Format("15:04:05"))
	case rate.Saving(now):
		return sRed.Render(fmt.Sprintf("API %s left · paused until %s", thousands(rate.Remaining), rate.Reset.Local().Format("15:04")))
	}
	if g := a.client.GraphQLRate(); g.Saving(now) {
		return roundPill("GraphQL "+thousands(g.Remaining)+" left", cAmber, tint[KRun])
	}
	return ""
}

// barKeys is how many of a screen's keys the key bar shows, before the
// actions key: the first five that apply, so a screen lists its most
// important keys first.
const barKeys = 5

// renderKeyBar shows the keys that matter on this screen, or a flash message.
// When not every key fits, the right end says how many more ? lists.
func (a *App) renderKeyBar() string {
	var parts []string
	all := a.hints(a.top().Keys(a))
	for i, h := range all {
		if i == barKeys {
			break
		}
		parts = append(parts, keycap(h.Key)+" "+sHint.Render(h.Desc))
	}
	// What the bar leaves out is in the action menu and on the help screen.
	leftOut := len(all) - len(parts)
	if len(all) > 0 {
		parts = append(parts, keycap(".")+" "+sHint.Render("menu"))
	}
	fitted := func(more int, refresh bool) (left, right string, shown int) {
		help := "all keys"
		if more == 1 {
			help = "1 more key"
		} else if more > 1 {
			help = fmt.Sprintf("%d more keys", more)
		}
		right = keycap("?") + " " + sHint.Render(help) + " "
		if _, ok := a.top().(explainer); ok {
			right = keycap("i") + " " + sHint.Render("explain") + "  " + right
		}
		if refresh {
			right = keycap("ctrl+r") + " " + sHint.Render("refresh") + "  " + right
		}
		if back := a.backTo(); len(a.stack) > 1 || back == "clear filter" {
			right = keycap("esc") + " " + sHint.Render(back) + "  " + right
		}
		if _, on := a.top().(*statusView); !on && len(a.openIncidents()) > 0 {
			right = keycap("ctrl+g") + " " + sAmber.Render("incident") + "  " + right
		}
		if a.flash != "" {
			right = a.flashKind.chip() + " " + sText.Render(a.flash)
			if n := a.unreadErrors; a.flashKind == nError && n > 0 {
				right += sDim.Render(fmt.Sprintf(" · %d unread", n))
			}
			right += " "
		}
		left = " "
		for _, p := range parts {
			if width(left)+width(p)+2+width(right) > a.w {
				break
			}
			left += p + "  "
			shown++
		}
		return left, right, shown
	}
	left, right, shown := fitted(leftOut, true)
	if shown < len(parts) {
		// Not every key fits: "refresh" makes way, and ? says how many are left.
		if left, right, shown = fitted(leftOut, false); shown < len(parts) {
			left, right, _ = fitted(leftOut+len(parts)-shown, false)
		}
	}
	if width(left)+width(right) > a.w {
		return fit(right, a.w)
	}
	return left + strings.Repeat(" ", a.w-width(left)-width(right)) + right
}

// openURL opens a page in the default browser.
func (a *App) openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if err := openBrowser(url); err != nil {
			return flashMsg{nError, "Could not open browser: " + err.Error()}
		}
		return flashMsg{nInfo, "Opened " + url}
	}
}

// copyText copies to the system clipboard.
func (a *App) copyText(text, what string) tea.Cmd {
	return func() tea.Msg {
		if err := writeClipboard(text); err != nil {
			return flashMsg{nError, "Clipboard unavailable: " + err.Error()}
		}
		return flashMsg{nDone, "Copied " + what}
	}
}

// repoByName finds a repo in the cached list.
func (a *App) repoByName(name string) (gh.Repo, bool) {
	for _, r := range a.repos {
		if strings.EqualFold(r.FullName, name) {
			return r, true
		}
	}
	return gh.Repo{}, false
}
