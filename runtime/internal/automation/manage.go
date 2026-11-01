package automation

import "time"

// Create saves a new automation. An enabled automation is first due one
// schedule step from now; nothing runs at the moment it is created.
func (s *Scheduler) Create(spec Spec, enabled bool) (Automation, error) {
	spec, err := Normalize(spec)
	if err != nil {
		return Automation{}, err
	}
	now := s.options.Now()
	automation := Automation{ID: randomHex(4), Spec: spec, Enabled: enabled, CreatedAtMS: now.UnixMilli(), UpdatedAtMS: now.UnixMilli()}
	for _, taken := s.options.Store.Get(automation.ID); taken; _, taken = s.options.Store.Get(automation.ID) {
		automation.ID = randomHex(4)
	}
	if enabled {
		automation.NextRunAtMS = Next(spec.Schedule, now, s.options.Location).UnixMilli()
	}
	if err := s.options.Store.Insert(automation); err != nil {
		return Automation{}, err
	}
	s.Wake()
	return automation, nil
}

// Edit replaces an automation's spec. A changed schedule is due one step from
// now; an unchanged one keeps its next time.
func (s *Scheduler) Edit(id string, spec Spec) (Automation, error) {
	spec, err := Normalize(spec)
	if err != nil {
		return Automation{}, err
	}
	now := s.options.Now()
	saved, err := s.options.Store.Update(id, func(automation *Automation) error {
		rescheduled := automation.Schedule != spec.Schedule
		automation.Spec, automation.UpdatedAtMS = spec, now.UnixMilli()
		if automation.Enabled && (rescheduled || automation.NextRunAtMS == 0) {
			automation.NextRunAtMS = Next(spec.Schedule, now, s.options.Location).UnixMilli()
		}
		return nil
	})
	s.Wake()
	return saved, err
}

// SetEnabled pauses or resumes an automation. Resuming never runs missed
// slots: the next run is one schedule step from now.
func (s *Scheduler) SetEnabled(id string, enabled bool) (Automation, error) {
	now := s.options.Now()
	saved, err := s.options.Store.Update(id, func(automation *Automation) error {
		if automation.Enabled == enabled {
			return nil
		}
		automation.Enabled, automation.UpdatedAtMS, automation.NextRunAtMS = enabled, now.UnixMilli(), 0
		if enabled {
			automation.NextRunAtMS = Next(automation.Schedule, now, s.options.Location).UnixMilli()
		}
		return nil
	})
	s.Wake()
	return saved, err
}

// Remove deletes an automation and its history. A run already under way
// keeps running as an ordinary session.
func (s *Scheduler) Remove(id string) error {
	s.decide.Lock()
	defer s.decide.Unlock()
	return s.options.Store.Remove(id)
}

// Summary is the run history shape the sidebar reads.
type Summary struct {
	Total        int `json:"total"`
	Skipped      int `json:"skipped"`
	Running      int `json:"running"`
	NeedsInput   int `json:"needsInput"`
	RecentFailed int `json:"recentFailed"`
}

// recentFailureWindow is how long a failed run counts toward the badge.
const recentFailureWindow = 24 * time.Hour

// Summarize counts runs, optionally for one automation. Total counts the
// agents started; a skipped run started none.
func (s *Scheduler) Summarize(id string) Summary {
	cutoff := s.options.Now().Add(-recentFailureWindow).UnixMilli()
	var summary Summary
	for _, run := range s.options.Store.Runs(id) {
		if run.Outcome == OutcomeSkipped {
			summary.Skipped++
			continue
		}
		summary.Total++
		switch {
		case run.Outcome == OutcomeNeedsInput:
			summary.NeedsInput++
		case !run.Settled():
			summary.Running++
		case (run.Outcome == OutcomeFailed || run.Outcome == OutcomeUnknown) && run.StartedAtMS >= cutoff:
			summary.RecentFailed++
		}
	}
	return summary
}
