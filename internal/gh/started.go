package gh

// GitHub reports a run as queued again while a later job of it waits for a
// runner. The client remembers which attempt of a run has started, and
// reports that attempt as in progress until it completes.

const snapshotStarted = "started-runs"

// KeepStarted marks the runs that are in progress as started, and reports a
// started run that GitHub calls queued as in progress, with Requeued set.
func (c *Client) KeepStarted(runs []Run) {
	c.startedMu.Lock()
	defer c.startedMu.Unlock()
	c.loadStarted()
	changed := false
	for i := range runs {
		r := &runs[i]
		switch r.Status {
		case "in_progress":
			if c.started[r.ID] != r.RunAttempt {
				c.started[r.ID], changed = r.RunAttempt, true
			}
		case "queued":
			if c.started[r.ID] == r.RunAttempt && r.RunAttempt > 0 {
				r.Status, r.Requeued = "in_progress", true
			}
		case "completed":
			if _, ok := c.started[r.ID]; ok {
				delete(c.started, r.ID)
				changed = true
			}
		}
	}
	if changed {
		c.Remember(snapshotStarted, c.started)
	}
}

// keepStartedJobs marks the run of jobs as started once one of them has.
func (c *Client) keepStartedJobs(jobs []Job) {
	for _, j := range jobs {
		if j.Status == "in_progress" || j.Status == "completed" {
			c.KeepStarted([]Run{{ID: j.RunID, RunAttempt: j.RunAttempt, Status: "in_progress"}})
			return
		}
	}
}

// loadStarted reads the started runs of the last session, once.
func (c *Client) loadStarted() {
	if c.started != nil {
		return
	}
	c.started = map[int64]int{}
	c.Recall(snapshotStarted, &c.started)
}

// HasStarted reports whether the attempt of run is known to have started.
func (c *Client) HasStarted(r Run) bool {
	c.startedMu.Lock()
	defer c.startedMu.Unlock()
	c.loadStarted()
	return r.RunAttempt > 0 && c.started[r.ID] == r.RunAttempt
}
