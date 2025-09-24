/*
Copyright 2023 The Dapr Authors
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
	"time"
)

// ReminderTrack is a persisted object that keeps track of the last time a reminder fired.
type ReminderTrack struct {
	LastFiredTime  time.Time `json:"lastFiredTime"`
	RepetitionLeft int       `json:"repetitionLeft"`
	Etag           *string   `json:",omitempty"`
}

func (r *ReminderTrack) MarshalJSON() ([]byte, error) {
	type reminderTrackAlias ReminderTrack

	// Custom serializer that encodes times (LastFiredTime) in the RFC3339 format, for backwards-compatibility
	m := &struct {
		LastFiredTime string `json:"lastFiredTime,omitempty"`
		*reminderTrackAlias
	}{
		reminderTrackAlias: (*reminderTrackAlias)(r),
	}

	if !r.LastFiredTime.IsZero() {
		m.LastFiredTime = r.LastFiredTime.Format(time.RFC3339)
	}

	return json.Marshal(m)
}

func (r *ReminderTrack) UnmarshalJSON(data []byte) error {
	type reminderTrackAlias ReminderTrack

	// Parse RegisteredTime and ExpirationTime as dates in the RFC3339 format
	m := &struct {
		LastFiredTime string `json:"lastFiredTime"`
		*reminderTrackAlias
	}{
		reminderTrackAlias: (*reminderTrackAlias)(r),
	}
	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	if m.LastFiredTime != "" {
		r.LastFiredTime, err = time.Parse(time.RFC3339, m.LastFiredTime)
		if err != nil {
			return fmt.Errorf("failed to parse LastFiredTime: %w", err)
		}
		r.LastFiredTime = r.LastFiredTime.Truncate(time.Second)
	}

	return nil
}
