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

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ActorTypeReminders struct {
	Key            string
	Reminders      []ActorReminder
	IsBinary       bool
	Etag           string
	ExpirationTime *time.Time
	UpdateTime     *time.Time
}

type ActorReminder struct {
	RegisteredTime time.Time
	ExpirationTime time.Time
	Period         string
	Data           string
	ActorID        string
	ActorType      string
	Name           string
	DueTime        string
}

type internalActorTypeReminders struct {
	Key            string
	Value          string
	IsBinary       bool
	Etag           string
	ExpirationTime *time.Time
	UpdateTime     *time.Time
}

func (s *SQLite) ActorReminders(t *testing.T, ctx context.Context, actorType string) ActorTypeReminders {
	t.Helper()

	query := fmt.Sprintf("SELECT * FROM '%s' WHERE key = 'actors||%s'", s.tableName, actorType)
	rows, err := s.GetConnection(t).QueryContext(ctx, query)
	require.NoError(t, err)

	require.NoError(t, rows.Err())

	if !rows.Next() {
		return ActorTypeReminders{}
	}

	var r internalActorTypeReminders
	require.NoError(t, rows.Scan(&r.Key, &r.Value, &r.IsBinary, &r.Etag, &r.ExpirationTime, &r.UpdateTime))
	require.False(t, rows.Next(), "Reminder should only have a single entry per actor type")

	var reminders []ActorReminder
	require.NoError(t, json.Unmarshal([]byte(r.Value), &reminders))

	return ActorTypeReminders{
		Key:            r.Key,
		Reminders:      reminders,
		IsBinary:       r.IsBinary,
		Etag:           r.Etag,
		ExpirationTime: r.ExpirationTime,
		UpdateTime:     r.UpdateTime,
	}
}
