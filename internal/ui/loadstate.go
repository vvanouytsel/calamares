package ui

import "time"

// loadState is how reading something from GitHub went: when it was last
// read, when that was last tried, whether a read is going on, and the error
// of the last one. The store and the screens that load their own data all
// keep one per thing they read.
type loadState struct {
	at time.Time // when it was last read successfully
	// tried is when a read last started, successful or not: live updates
	// wait from there, so a failing read isn't retried every second.
	tried time.Time
	busy  bool
	err   error
}

// begin marks a read as started. It reports false, and changes nothing,
// while one is already going on.
func (l *loadState) begin() bool {
	if l.busy {
		return false
	}
	l.busy, l.tried = true, time.Now()
	return true
}

// end records how the read went.
func (l *loadState) end(err error) {
	l.busy, l.err = false, err
	if err == nil {
		l.at = time.Now()
	}
}

// waited reports whether d has passed since a read was last started.
func (l *loadState) waited(d time.Duration) bool { return time.Since(l.tried) >= d }
