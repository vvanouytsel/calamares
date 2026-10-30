package ui

import (
	"fmt"
	"slices"
	"time"

	"github.com/vvanouytsel/calamares/internal/gh"
)

// Comments on lines of the diff are held as pending, kept between sessions,
// and go out together with the review you submit: one notification for the
// author instead of one per comment.

func pendingKey(repo string, number int) string { return fmt.Sprintf("review:%s#%d", repo, number) }

// pendingFor lists the line comments you have added to your review of a pull request.
func (a *App) pendingFor(repo string, number int) []gh.DraftComment {
	k := pendingKey(repo, number)
	if list, ok := a.pending[k]; ok {
		return list
	}
	var list []gh.DraftComment
	a.client.Recall(k, &list)
	a.pending[k] = list
	return list
}

func (a *App) setPending(repo string, number int, list []gh.DraftComment) {
	k := pendingKey(repo, number)
	a.pending[k] = list
	a.client.Remember(k, list)
}

func (a *App) addPending(repo string, number int, c gh.DraftComment) {
	a.setPending(repo, number, append(slices.Clone(a.pendingFor(repo, number)), c))
}

// pendingAsComments shows the pending comments in the diff, as comments of yours.
func (a *App) pendingAsComments(repo string, number int) []gh.Comment {
	var out []gh.Comment
	for i, c := range a.pendingFor(repo, number) {
		out = append(out, gh.Comment{ID: int64(-1 - i), Author: a.me + " · pending", Body: c.Body, Path: c.Path, Line: c.Line, Side: c.Side,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond)})
	}
	return out
}
