package core

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/actors/core/reminder"
	timeutils "github.com/dapr/kit/time"
)

// Reminder represents a reminder or timer for a unique actor.
type Reminder struct {
	ActorID        string                  `json:"actorID,omitempty"`
	ActorType      string                  `json:"actorType,omitempty"`
	Name           string                  `json:"name,omitempty"`
	Data           json.RawMessage         `json:"data,omitempty"`
	Period         reminder.ReminderPeriod `json:"period,omitempty"`
	RegisteredTime time.Time               `json:"registeredTime,omitempty"`
	DueTime        string                  `json:"dueTime,omitempty"` // Exact input value from user
	ExpirationTime time.Time               `json:"expirationTime,omitempty"`
	Callback       string                  `json:"callback,omitempty"` // Used by timers only
}

// ActorKey returns the key of the actor for this reminder.
func (r Reminder) ActorKey() string {
	return r.ActorType + reminder.DaprSeparator + r.ActorID
}

// Key returns the key for this unique reminder.
func (r Reminder) Key() string {
	return r.ActorType + reminder.DaprSeparator + r.ActorID + reminder.DaprSeparator + r.Name
}

// NextTick returns the time the reminder should tick again next.
func (r Reminder) NextTick() time.Time {
	return r.RegisteredTime
}

// HasRepeats returns true if the reminder has repeats left.
func (r Reminder) HasRepeats() bool {
	return r.Period.HasRepeats()
}

// RepeatsLeft returns the number of repeats left.
func (r Reminder) RepeatsLeft() int {
	return r.Period.Repeats
}

// TickExecuted should be called after a reminder has been executed.
// "done" will be true if the reminder is done, i.e. no more executions should happen.
// If the reminder is not done, call "NextTick" to get the time it should tick next.
// Note: this method is not concurrency-safe.
func (r *Reminder) TickExecuted() (done bool) {
	if r.Period.Repeats > 0 {
		r.Period.Repeats--
	}

	if !r.HasRepeats() {
		return true
	}

	r.RegisteredTime = r.Period.GetFollowing(r.RegisteredTime)

	return false
}

// UpdateFromTrack updates the reminder with data from the track object.
func (r *Reminder) UpdateFromTrack(track *reminder.ReminderTrack) {
	if track == nil || track.LastFiredTime.IsZero() {
		return
	}

	r.Period.Repeats = track.RepetitionLeft
	r.RegisteredTime = r.Period.GetFollowing(track.LastFiredTime)
}

func (r *Reminder) MarshalJSON() ([]byte, error) {
	type reminderAlias Reminder

	// Custom serializer that encodes times (RegisteredTime and ExpirationTime) in the RFC3339 format.
	// Also adds a custom serializer for Period to omit empty strings.
	// This is for backwards-compatibility and also because we don't need to store precision with less than seconds
	m := struct {
		RegisteredTime string           `json:"registeredTime,omitempty"`
		ExpirationTime string           `json:"expirationTime,omitempty"`
		Period         string           `json:"period,omitempty"`
		Data           *json.RawMessage `json:"data,omitempty"`
		*reminderAlias
	}{
		reminderAlias: (*reminderAlias)(r),
	}

	if !r.RegisteredTime.IsZero() {
		m.RegisteredTime = r.RegisteredTime.Format(time.RFC3339)
	}
	if !r.ExpirationTime.IsZero() {
		m.ExpirationTime = r.ExpirationTime.Format(time.RFC3339)
	}

	m.Period = r.Period.String()
	if len(r.Data) > 0 {
		m.Data = &r.Data
	}

	return json.Marshal(m)
}

func (r *Reminder) UnmarshalJSON(data []byte) error {
	type reminderAlias Reminder

	*r = Reminder{
		Period: reminder.NewEmptyReminderPeriod(),
	}

	// Parse RegisteredTime and ExpirationTime as dates in the RFC3339 format
	m := &struct {
		ExpirationTime string `json:"expirationTime"`
		RegisteredTime string `json:"registeredTime"`
		*reminderAlias
	}{
		reminderAlias: (*reminderAlias)(r),
	}
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	if m.RegisteredTime != "" {
		r.RegisteredTime, err = time.Parse(time.RFC3339, m.RegisteredTime)
		if err != nil {
			return fmt.Errorf("failed to parse RegisteredTime: %w", err)
		}
		r.RegisteredTime = r.RegisteredTime.Truncate(time.Second)
	}

	if m.ExpirationTime != "" {
		r.ExpirationTime, err = time.Parse(time.RFC3339, m.ExpirationTime)
		if err != nil {
			return fmt.Errorf("failed to parse ExpirationTime: %w", err)
		}
		r.ExpirationTime = r.ExpirationTime.Truncate(time.Second)
	}

	return nil
}

// String implements fmt.Stringer and is used for debugging.
func (r Reminder) String() string {
	hasData := r.Data != nil
	dueTime := "nil"
	if !r.RegisteredTime.IsZero() {
		dueTime = "'" + r.RegisteredTime.Format(time.RFC3339) + "'"
	}
	expirationTime := "nil"
	if !r.ExpirationTime.IsZero() {
		expirationTime = "'" + r.ExpirationTime.Format(time.RFC3339) + "'"
	}

	return fmt.Sprintf(
		"name='%s' hasData=%v period='%s' dueTime=%s expirationTime=%s",
		r.Key(), hasData, r.Period, dueTime, expirationTime,
	)
}

// ParseTimeTruncateSeconds is a wrapper around timeutils.ParseTime that truncates the time to seconds.
func ParseTimeTruncateSeconds(val string, now *time.Time) (time.Time, error) {
	t, err := timeutils.ParseTime(val, now)
	if err != nil {
		return t, err
	}
	t = t.Truncate(time.Second)
	return t, nil
}
