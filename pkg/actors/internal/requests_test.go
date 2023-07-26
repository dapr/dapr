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
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewReminderFromCreateReminderRequest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name         string
		req          func(r *CreateReminderRequest)
		wantReminder func(r *Reminder)
		wantErr      bool
	}{
		{
			name:         "base test",
			req:          func(r *CreateReminderRequest) { return },
			wantReminder: func(r *Reminder) { return },
		},
		{
			name: "with data",
			req: func(r *CreateReminderRequest) {
				r.Data = json.RawMessage(`"hi"`)
			},
			wantReminder: func(r *Reminder) {
				r.Data = json.RawMessage(`"hi"`)
			},
		},
		{
			name: "with data as JSON object",
			req: func(r *CreateReminderRequest) {
				r.Data = json.RawMessage(`{  "foo": [ 12, 4 ] } `)
			},
			wantReminder: func(r *Reminder) {
				// Gets compacted automatically
				r.Data = json.RawMessage(`{"foo":[12,4]}`)
			},
		},
		{
			name: "with period",
			req: func(r *CreateReminderRequest) {
				r.Period = "2s"
			},
			wantReminder: func(r *Reminder) {
				r.Period, _ = NewReminderPeriod("2s")
			},
		},
		{
			name: "with due time as duration",
			req: func(r *CreateReminderRequest) {
				r.DueTime = "2m"
			},
			wantReminder: func(r *Reminder) {
				r.DueTime = "2m"
				r.RegisteredTime = r.RegisteredTime.Add(2 * time.Minute)
			},
		},
		{
			name: "with due time as absolute",
			req: func(r *CreateReminderRequest) {
				r.DueTime = now.Add(10 * time.Minute).Format(time.RFC3339)
			},
			wantReminder: func(r *Reminder) {
				r.DueTime = now.Add(10 * time.Minute).Format(time.RFC3339)
				r.RegisteredTime = now.Add(10 * time.Minute)
			},
		},
		{
			name: "with TTL as duration",
			req: func(r *CreateReminderRequest) {
				r.TTL = "10m"
			},
			wantReminder: func(r *Reminder) {
				r.ExpirationTime = now.Add(10 * time.Minute)
			},
		},
		{
			name: "with TTL as absolute",
			req: func(r *CreateReminderRequest) {
				r.TTL = now.Add(10 * time.Minute).Format(time.RFC3339)
			},
			wantReminder: func(r *Reminder) {
				r.ExpirationTime = now.Add(10 * time.Minute)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Base request and wantReminder, to keep it DRY
			req := &CreateReminderRequest{
				ActorID:   "id",
				ActorType: "type",
				Name:      "name",
			}
			tt.req(req)
			wantReminder := &Reminder{
				ActorID:        "id",
				ActorType:      "type",
				Name:           "name",
				Period:         NewEmptyReminderPeriod(),
				RegisteredTime: now,
			}
			tt.wantReminder(wantReminder)

			// Run tests
			gotReminder, err := req.NewReminder(now)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewReminderFromCreateReminderRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotReminder, wantReminder) {
				t.Errorf("NewReminderFromCreateReminderRequest() = %#v, want %#v", gotReminder, wantReminder)
			}
		})
	}

	t.Run("TTL in the past", func(t *testing.T) {
		req := &CreateReminderRequest{
			ActorID:   "id",
			ActorType: "type",
			Name:      "name",
			TTL:       "2002-02-02T12:00:02Z", // In the past
		}
		_, err := req.NewReminder(now)
		require.Error(t, err)
		require.ErrorContains(t, err, "has already expired")
	})
}

func TestNewReminderFromCreateTimerRequest(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name         string
		req          func(r *CreateTimerRequest)
		wantReminder func(r *Reminder)
		wantErr      bool
	}{
		{
			name:         "base test",
			req:          func(r *CreateTimerRequest) { return },
			wantReminder: func(r *Reminder) { return },
		},
		{
			name: "with data",
			req: func(r *CreateTimerRequest) {
				r.Data = json.RawMessage(`"hi"`)
			},
			wantReminder: func(r *Reminder) {
				r.Data = json.RawMessage(`"hi"`)
			},
		},
		{
			name: "with data as JSON object",
			req: func(r *CreateTimerRequest) {
				r.Data = json.RawMessage(`{  "foo": [ 12, 4 ] } `)
			},
			wantReminder: func(r *Reminder) {
				// Gets compacted automatically
				r.Data = json.RawMessage(`{"foo":[12,4]}`)
			},
		},
		{
			name: "with period",
			req: func(r *CreateTimerRequest) {
				r.Period = "2s"
			},
			wantReminder: func(r *Reminder) {
				r.Period, _ = NewReminderPeriod("2s")
			},
		},
		{
			name: "with due time as duration",
			req: func(r *CreateTimerRequest) {
				r.DueTime = "2m"
			},
			wantReminder: func(r *Reminder) {
				r.DueTime = "2m"
				r.RegisteredTime = r.RegisteredTime.Add(2 * time.Minute)
			},
		},
		{
			name: "with due time as absolute",
			req: func(r *CreateTimerRequest) {
				r.DueTime = now.Add(10 * time.Minute).Format(time.RFC3339)
			},
			wantReminder: func(r *Reminder) {
				r.DueTime = now.Add(10 * time.Minute).Format(time.RFC3339)
				r.RegisteredTime = now.Add(10 * time.Minute)
			},
		},
		{
			name: "with TTL as duration",
			req: func(r *CreateTimerRequest) {
				r.TTL = "10m"
			},
			wantReminder: func(r *Reminder) {
				r.ExpirationTime = now.Add(10 * time.Minute)
			},
		},
		{
			name: "with TTL as absolute",
			req: func(r *CreateTimerRequest) {
				r.TTL = now.Add(10 * time.Minute).Format(time.RFC3339)
			},
			wantReminder: func(r *Reminder) {
				r.ExpirationTime = now.Add(10 * time.Minute)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Base request and wantReminder, to keep it DRY
			req := &CreateTimerRequest{
				ActorID:   "id",
				ActorType: "type",
				Name:      "name",
			}
			tt.req(req)
			wantReminder := &Reminder{
				ActorID:        "id",
				ActorType:      "type",
				Name:           "name",
				Period:         NewEmptyReminderPeriod(),
				RegisteredTime: now,
			}
			tt.wantReminder(wantReminder)

			// Run tests
			gotReminder, err := req.NewReminder(now)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewReminderFromCreateTimerRequest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotReminder, wantReminder) {
				t.Errorf("NewReminderFromCreateTimerRequest() = %#v, want %#v", gotReminder, wantReminder)
			}
		})
	}

	t.Run("TTL in the past", func(t *testing.T) {
		req := &CreateTimerRequest{
			ActorID:   "id",
			ActorType: "type",
			Name:      "name",
			TTL:       "2002-02-02T12:00:02Z", // In the past
		}
		_, err := req.NewReminder(now)
		require.Error(t, err)
		require.ErrorContains(t, err, "has already expired")
	})
}
