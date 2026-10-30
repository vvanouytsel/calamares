package ui

import tea "github.com/charmbracelet/bubbletea"

// Screen is the app as Bubble Tea runs it. Bubble Tea asks for the screen
// after every message, and the animation sends four a second; Screen draws
// the app again only when something happened, or when what is on display
// moves with the animation (a spinner, a running run).
type Screen struct {
	app    *App
	last   string
	stale  bool // something happened since last was drawn
	moving bool // last has something on it that moves with the animation
}

// NewScreen wraps the app for tea.NewProgram.
func NewScreen(a *App) *Screen { return &Screen{app: a, stale: true} }

func (s *Screen) Init() tea.Cmd { return s.app.Init() }

func (s *Screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, tick := msg.(animMsg); !tick || s.moving {
		s.stale = true
	}
	_, cmd := s.app.Update(msg)
	return s, cmd
}

func (s *Screen) View() string {
	if s.stale {
		animated = false
		s.last = s.app.View()
		s.moving, s.stale = animated, false
	}
	return s.last
}
