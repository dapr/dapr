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
	"time"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// GetReminderRequest is the request object to get an existing reminder.
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// CreateReminderRequest is the request object to create a new reminder.
//
//nolint:recvcheck
type CreateReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
	Data      *anypb.Any `json:"data"`
	DueTime   string     `json:"dueTime"`
	Period    string     `json:"period"`
	TTL       string     `json:"ttl"`
	IsOneShot bool       `json:"-"`
}

// ActorKey returns the key of the actor for this reminder.
func (req CreateReminderRequest) ActorKey() string {
	return req.ActorType + DaprSeparator + req.ActorID
}

// Key returns the key for this unique reminder.
func (req CreateReminderRequest) Key() string {
	return req.ActorType + DaprSeparator + req.ActorID + DaprSeparator + req.Name
}

// NewReminder returns a new Reminder from a CreateReminderRequest object.
func (req CreateReminderRequest) NewReminder(now time.Time, truncate bool) (reminder *Reminder, err error) {
	reminder = &Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.Name,
		Data:      req.Data,
	}

	err = setReminderTimes(reminder, req.DueTime, req.Period, req.TTL, now, "reminder", truncate)
	if err != nil {
		return nil, err
	}

	return reminder, nil
}

func (req *CreateReminderRequest) UnmarshalJSON(data []byte) error {
	type createReminderAlias CreateReminderRequest

	*req = CreateReminderRequest{}

	m := &struct {
		Data json.RawMessage `json:"data"`
		*createReminderAlias
	}{
		createReminderAlias: (*createReminderAlias)(req),
	}

	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	if len(m.Data) > 0 {
		req.Data, err = anypb.New(wrapperspb.Bytes(m.Data))
		if err != nil {
			return fmt.Errorf("failed to unmarshal data: %w", err)
		}
	}

	return nil
}

// CreateTimerRequest is the request object to create a new timer.
//
//nolint:recvcheck
type CreateTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
	DueTime   string     `json:"dueTime"`
	Period    string     `json:"period"`
	TTL       string     `json:"ttl"`
	Callback  string     `json:"callback"`
	Data      *anypb.Any `json:"data"`
}

// ActorKey returns the key of the actor for this timer.
func (req CreateTimerRequest) ActorKey() string {
	return req.ActorType + DaprSeparator + req.ActorID
}

// Key returns the key for this unique timer.
func (req CreateTimerRequest) Key() string {
	return req.ActorType + DaprSeparator + req.ActorID + DaprSeparator + req.Name
}

// NewReminder returns a new Timer from a CreateTimerRequest object.
func (req CreateTimerRequest) NewReminder(now time.Time, truncate bool) (reminder *Reminder, err error) {
	reminder = &Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Name:      req.Name,
		Callback:  req.Callback,
		Data:      req.Data,
	}

	err = setReminderTimes(reminder, req.DueTime, req.Period, req.TTL, now, "timer", truncate)
	if err != nil {
		return nil, err
	}

	return reminder, nil
}

func (req *CreateTimerRequest) UnmarshalJSON(data []byte) error {
	type createTimerAlias CreateTimerRequest

	*req = CreateTimerRequest{}

	m := &struct {
		Data json.RawMessage `json:"data"`
		*createTimerAlias
	}{
		createTimerAlias: (*createTimerAlias)(req),
	}

	err := json.Unmarshal(data, &m)
	if err != nil {
		return err
	}

	if len(m.Data) > 0 {
		req.Data, err = anypb.New(wrapperspb.Bytes(m.Data))
		if err != nil {
			return fmt.Errorf("failed to unmarshal data: %w", err)
		}
	}

	return nil
}

func setReminderTimes(reminder *Reminder, dueTime string, period string, ttl string, now time.Time, logMsg string, truncate bool) (err error) {
	// Due time and registered time
	reminder.RegisteredTime = now
	reminder.DueTime = dueTime
	if dueTime != "" {
		reminder.RegisteredTime, err = parseTimeTruncateSeconds(dueTime, &now, truncate)
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
		reminder.ExpirationTime, err = parseTimeTruncateSeconds(ttl, &reminder.RegisteredTime, truncate)
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
	return req.ActorType + DaprSeparator + req.ActorID
}

// Key returns the key for this unique reminder.
func (req DeleteReminderRequest) Key() string {
	return req.ActorType + DaprSeparator + req.ActorID + DaprSeparator + req.Name
}

type ListRemindersRequest struct {
	ActorType string
}

// DeleteTimerRequest is a request object for deleting a timer.
type DeleteTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// ActorKey returns the key of the actor for this timer.
func (req DeleteTimerRequest) ActorKey() string {
	return req.ActorType + DaprSeparator + req.ActorID
}

// Key returns the key for this unique timer.
func (req DeleteTimerRequest) Key() string {
	return req.ActorType + DaprSeparator + req.ActorID + DaprSeparator + req.Name
}
