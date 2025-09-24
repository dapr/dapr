/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	timeutils "github.com/dapr/kit/time"
)

// Reminder represents a reminder or timer for a unique actor.
//
//nolint:recvcheck
type Reminder struct {
	ActorID        string         `json:"actorID,omitempty"`
	ActorType      string         `json:"actorType,omitempty"`
	Name           string         `json:"name,omitempty"`
	Data           *anypb.Any     `json:"data,omitempty"`
	Period         ReminderPeriod `json:"period,omitempty"`
	RegisteredTime time.Time      `json:"registeredTime,omitempty"`
	DueTime        string         `json:"dueTime,omitempty"` // Exact input value from user
	ExpirationTime time.Time      `json:"expirationTime,omitempty"`
	Callback       string         `json:"callback,omitempty"` // Used by timers only
	IsTimer        bool           `json:"-"`
	IsRemote       bool           `json:"-"`
	SkipLock       bool           `json:"-"`
}

// ActorKey returns the key of the actor for this reminder.
func (r Reminder) ActorKey() string {
	return r.ActorType + DaprSeparator + r.ActorID
}

// Key returns the key for this unique reminder.
func (r Reminder) Key() string {
	return r.ActorType + DaprSeparator + r.ActorID + DaprSeparator + r.Name
}

// NextTick returns the time the reminder should tick again next.
// If the reminder has a TTL and the next tick is beyond the TTL, the second returned value will be false.
func (r Reminder) NextTick() (time.Time, bool) {
	active := r.ExpirationTime.IsZero() || r.RegisteredTime.Before(r.ExpirationTime)
	return r.RegisteredTime, active
}

// HasRepeats returns true if the reminder has repeats left.
func (r Reminder) HasRepeats() bool {
	return r.Period.HasRepeats()
}

// RepeatsLeft returns the number of repeats left.
func (r Reminder) RepeatsLeft() int {
	return r.Period.repeats
}

// TickExecuted should be called after a reminder has been executed.
// "done" will be true if the reminder is done, i.e. no more executions should happen.
// If the reminder is not done, call "NextTick" to get the time it should tick next.
// Note: this method is not concurrency-safe.
func (r *Reminder) TickExecuted() (done bool) {
	if r.Period.repeats > 0 {
		r.Period.repeats--
	}

	if !r.HasRepeats() {
		return true
	}

	r.RegisteredTime = r.Period.GetFollowing(r.RegisteredTime)

	return false
}

// UpdateFromTrack updates the reminder with data from the track object.
func (r *Reminder) UpdateFromTrack(track *ReminderTrack) {
	if track == nil || track.LastFiredTime.IsZero() {
		return
	}

	r.Period.repeats = track.RepetitionLeft
	r.RegisteredTime = r.Period.GetFollowing(track.LastFiredTime)
}

// ScheduledTime returns the time the reminder is scheduled to be executed at.
// This is implemented to comply with the queueable interface.
func (r Reminder) ScheduledTime() time.Time {
	return r.RegisteredTime
}

func (r *Reminder) MarshalJSON() ([]byte, error) {
	type reminderAlias Reminder

	// Custom serializer that encodes times (RegisteredTime and ExpirationTime) in the RFC3339 format.
	// Also adds a custom serializer for Period to omit empty strings.
	// This is for backwards-compatibility and also because we don't need to store precision with less than seconds
	m := struct {
		RegisteredTime string          `json:"registeredTime,omitempty"`
		ExpirationTime string          `json:"expirationTime,omitempty"`
		Period         string          `json:"period,omitempty"`
		Data           json.RawMessage `json:"data,omitempty"`
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
	if r.Data != nil {
		msg, err := r.Data.UnmarshalNew()
		if err != nil {
			return nil, err
		}
		switch mm := msg.(type) {
		case *wrapperspb.BytesValue:
			m.Data = mm.GetValue()
		default:
			d, err := protojson.Marshal(mm)
			if err != nil {
				return nil, err
			}
			m.Data = json.RawMessage(d)
		}
	}

	return json.Marshal(m)
}

func (r *Reminder) UnmarshalJSON(data []byte) error {
	type reminderAlias Reminder

	*r = Reminder{
		Period: NewEmptyReminderPeriod(),
	}

	// Parse RegisteredTime and ExpirationTime as dates in the RFC3339 format
	m := &struct {
		ExpirationTime string          `json:"expirationTime"`
		RegisteredTime string          `json:"registeredTime"`
		Data           json.RawMessage `json:"data"`
		*reminderAlias
	}{
		reminderAlias: (*reminderAlias)(r),
	}
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	if len(m.Data) > 0 {
		r.Data, err = anypb.New(wrapperspb.Bytes(m.Data))
		if err != nil {
			return fmt.Errorf("failed to unmarshal data: %w", err)
		}
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

// MarshalBSON implements bson.Marshaler.
// It encodes the message into a map[string]any before calling bson.Marshal.
func (r *Reminder) MarshalBSON() ([]byte, error) {
	// We do this to make sure that the custom MarshalJSON above is invoked.
	// This round-trip via JSON is not great, but it works.
	j, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var dec map[string]any
	err = json.Unmarshal(j, &dec)
	if err != nil {
		return nil, err
	}
	return bson.Marshal(dec)
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
	period := r.Period.String()
	if period == "" {
		period = "nil"
	} else {
		period = "'" + period + "'"
	}

	return fmt.Sprintf(
		"name='%s' hasData=%t period=%s dueTime=%s expirationTime=%s",
		r.Key(), hasData, period, dueTime, expirationTime,
	)
}

func (r *Reminder) RequiresUpdating(new *Reminder) bool {
	// If the reminder is different, short-circuit
	if r.ActorID != new.ActorID ||
		r.ActorType != new.ActorType ||
		r.Name != new.Name {
		return false
	}

	return r.DueTime != new.DueTime ||
		r.Period != new.Period ||
		!new.ExpirationTime.IsZero() ||
		(!r.ExpirationTime.IsZero() && new.ExpirationTime.IsZero()) ||
		!reflect.DeepEqual(r.Data, new.Data)
}

// parseTimeTruncateSeconds is a wrapper around timeutils.ParseTime that truncates the time to seconds.
func parseTimeTruncateSeconds(val string, now *time.Time, truncate bool) (time.Time, error) {
	t, err := timeutils.ParseTime(val, now)
	if err != nil {
		return t, err
	}
	if truncate {
		t = t.Truncate(time.Second)
	}
	return t, nil
}
