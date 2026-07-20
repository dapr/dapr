/*
Copyright 2026 The Dapr Authors
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

package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/cron"
)

// Mirrors the parser options go-etcd-cron uses for job schedules.
func scheduleParser() cron.Parser {
	return cron.NewParser(cron.Second |
		cron.Minute |
		cron.Hour |
		cron.Dom |
		cron.Month |
		cron.Dow |
		cron.Descriptor,
	)
}

// TestScheduleTimezoneDST tests a CRON_TZ= schedule holds its local wall clock
// across a DST transition, shifting the UTC instant by itself.
func TestScheduleTimezoneDST(t *testing.T) {
	sched, err := scheduleParser().Parse("CRON_TZ=Europe/Rome 0 0 9 * * *")
	require.NoError(t, err)

	rome, err := time.LoadLocation("Europe/Rome")
	require.NoError(t, err)

	next := time.Date(2026, 10, 23, 12, 0, 0, 0, rome)
	for _, exp := range []struct {
		local string
		utc   string
	}{
		{"2026-10-24T09:00:00+02:00", "2026-10-24T07:00:00Z"},
		{"2026-10-25T09:00:00+01:00", "2026-10-25T08:00:00Z"},
		{"2026-10-26T09:00:00+01:00", "2026-10-26T08:00:00Z"},
	} {
		next = sched.Next(next)
		assert.Equal(t, exp.local, next.In(rome).Format(time.RFC3339))
		assert.Equal(t, exp.utc, next.UTC().Format(time.RFC3339))
	}
}

func TestScheduleTimezonePrefixes(t *testing.T) {
	parser := scheduleParser()

	t.Run("TZ and CRON_TZ are equivalent", func(t *testing.T) {
		start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

		cronTZ, err := parser.Parse("CRON_TZ=Asia/Kolkata 0 0 9 * * *")
		require.NoError(t, err)
		tz, err := parser.Parse("TZ=Asia/Kolkata 0 0 9 * * *")
		require.NoError(t, err)

		assert.Equal(t, cronTZ.Next(start), tz.Next(start))
	})

	t.Run("the prefix works with descriptors", func(t *testing.T) {
		_, err := parser.Parse("CRON_TZ=Europe/Rome @daily")
		require.NoError(t, err)
	})

	t.Run("an unknown zone is rejected", func(t *testing.T) {
		_, err := parser.Parse("CRON_TZ=Not/AZone 0 0 9 * * *")
		require.ErrorContains(t, err, "provided bad location")
	})

	t.Run("no prefix means the scheduler's local zone", func(t *testing.T) {
		sched, err := parser.Parse("0 0 9 * * *")
		require.NoError(t, err)

		next := sched.Next(time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local))
		hour, _, _ := next.In(time.Local).Clock()
		assert.Equal(t, 9, hour)
	})
}

func TestScheduleTimezoneMalformedPrefix(t *testing.T) {
	parser := scheduleParser()

	for _, spec := range []string{"TZ=UTC", "CRON_TZ=Europe/Rome"} {
		t.Run(spec, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := parser.Parse(spec)
				assert.Error(t, err)
			})
		})
	}
}
