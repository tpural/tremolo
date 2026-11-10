package alerts

import (
	"errors"
	"fmt"
	"time"
)

// Healthy says whether rules are running: each has run within three of its intervals,
// and not every one failed its last run. A few failing rules are theirs to show, not
// a reason to call alerting down.
func (e *Engine) Healthy() error {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.rules) == 0 {
		if e.loadErr != "" {
			return fmt.Errorf("no rules loaded: %s", e.loadErr)
		}
		return errors.New("no rules loaded")
	}
	failed, lastErr := 0, ""
	for _, st := range e.rules {
		late := 3*st.Every + 30*time.Second
		last := st.LastRun
		if last.IsZero() {
			last = st.loaded
		}
		if now.Sub(last) > late {
			return fmt.Errorf("rule %s hasn't run since %s", st.Name, last.Format(time.RFC3339))
		}
		if st.Error != "" {
			failed++
			lastErr = st.Name + ": " + st.Error
		}
	}
	if failed == len(e.rules) {
		return fmt.Errorf("every rule failed its last run (%s)", lastErr)
	}
	return nil
}
