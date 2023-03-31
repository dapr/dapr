/*
Copyright 2021 The Dapr Authors
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

package reminders

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// CreateReminderRequest is the request object to create a new reminder.
type CreateReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
	Data      json.RawMessage `json:"data"`
	DueTime   string          `json:"dueTime"`
	Period    string          `json:"period"`
	TTL       string          `json:"ttl"`
}

// CreateTimerRequest is the request object to create a new timer.
type CreateTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
	DueTime   string          `json:"dueTime"`
	Period    string          `json:"period"`
	TTL       string          `json:"ttl"`
	Callback  string          `json:"callback"`
	Data      json.RawMessage `json:"data"`
}

// NewReminderFromCreateReminderRequest returns a new Reminder from a CreateReminderRequest object.
func NewReminderFromCreateReminderRequest(req *CreateReminderRequest, now time.Time) (reminder *Reminder, err error) {
	reminder = &Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.Name,
	}

	err = setReminderData(reminder, req.Data, "reminder")
	if err != nil {
		return nil, err
	}

	err = setReminderTimes(reminder, req.DueTime, req.Period, req.TTL, now, "reminder")
	if err != nil {
		return nil, err
	}

	return reminder, nil
}

// NewReminderFromCreateTimerRequest returns a new Timer from a CreateTimerRequest object.
func NewReminderFromCreateTimerRequest(req *CreateTimerRequest, now time.Time) (reminder *Reminder, err error) {
	reminder = &Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.Name,
		Callback:  req.Callback,
	}

	err = setReminderData(reminder, req.Data, "timer")
	if err != nil {
		return nil, err
	}

	err = setReminderTimes(reminder, req.DueTime, req.Period, req.TTL, now, "timer")
	if err != nil {
		return nil, err
	}

	return reminder, nil
}

func setReminderData(reminder *Reminder, data json.RawMessage, logMsg string) error {
	if len(data) == 0 {
		return nil
	}

	// Compact the data before setting it
	buf := &bytes.Buffer{}
	err := json.Compact(buf, data)
	if err != nil {
		return fmt.Errorf("failed to compact %s data: %w", logMsg, err)
	}

	if buf.Len() > 0 {
		reminder.Data = buf.Bytes()
	}

	return nil
}

func setReminderTimes(reminder *Reminder, dueTime string, period string, ttl string, now time.Time, logMsg string) (err error) {
	// Due time and registered time
	reminder.RegisteredTime = now
	reminder.DueTime = dueTime
	if dueTime != "" {
		reminder.RegisteredTime, err = parseTimeTruncateSeconds(dueTime, &now)
		if err != nil {
			return fmt.Errorf("error parsing %s due time: %w", logMsg, err)
		}
	}

	// Parse period and check correctness
	reminder.Period, err = NewReminderPeriod(period)
	if err != nil {
		return fmt.Errorf("invalid %s period: %w", logMsg, err)
	}

	// Set expiration time if configured
	if ttl != "" {
		reminder.ExpirationTime, err = parseTimeTruncateSeconds(ttl, &reminder.RegisteredTime)
		if err != nil {
			return fmt.Errorf("error parsing %s TTL: %w", logMsg, err)
		}
		// check if already expired
		if now.After(reminder.ExpirationTime) || reminder.RegisteredTime.After(reminder.ExpirationTime) {
			return fmt.Errorf("%s %s has already expired: dueTime: %s TTL: %s",
				logMsg, reminder.Key(), reminder.RegisteredTime, ttl)
		}
	}

	return nil
}
