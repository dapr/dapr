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

package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// GetReminderRequest is the request object to get an existing reminder.
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

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

// ActorKey returns the key of the actor for this reminder.
func (req CreateReminderRequest) ActorKey() string {
	return req.ActorType + daprSeparator + req.ActorID
}

// Key returns the key for this unique reminder.
func (req CreateReminderRequest) Key() string {
	return req.ActorType + daprSeparator + req.ActorID + daprSeparator + req.Name
}

// NewReminder returns a new Reminder from a CreateReminderRequest object.
func (req CreateReminderRequest) NewReminder(now time.Time) (reminder *Reminder, err error) {
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

// ActorKey returns the key of the actor for this timer.
func (req CreateTimerRequest) ActorKey() string {
	return req.ActorType + daprSeparator + req.ActorID
}

// Key returns the key for this unique timer.
func (req CreateTimerRequest) Key() string {
	return req.ActorType + daprSeparator + req.ActorID + daprSeparator + req.Name
}

// NewReminder returns a new Timer from a CreateTimerRequest object.
func (req CreateTimerRequest) NewReminder(now time.Time) (reminder *Reminder, err error) {
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

// DeleteReminderRequest is the request object for deleting a reminder.
type DeleteReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// ActorKey returns the key of the actor for this reminder.
func (req DeleteReminderRequest) ActorKey() string {
	return req.ActorType + daprSeparator + req.ActorID
}

// Key returns the key for this unique reminder.
func (req DeleteReminderRequest) Key() string {
	return req.ActorType + daprSeparator + req.ActorID + daprSeparator + req.Name
}

// DeleteTimerRequest is a request object for deleting a timer.
type DeleteTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// ActorKey returns the key of the actor for this timer.
func (req DeleteTimerRequest) ActorKey() string {
	return req.ActorType + daprSeparator + req.ActorID
}

// Key returns the key for this unique timer.
func (req DeleteTimerRequest) Key() string {
	return req.ActorType + daprSeparator + req.ActorID + daprSeparator + req.Name
}
