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
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReminderPeriod(t *testing.T) {
	t.Run("empty value", func(t *testing.T) {
		p, err := NewReminderPeriod("")
		require.NoError(t, err)
		expect := ReminderPeriod{
			value:   "",
			repeats: -1,
		}
		assert.Truef(t, reflect.DeepEqual(p, expect), "Got: '%#v' Expected: '%#v'", p, expect)
	})

	t.Run("interval in Go format", func(t *testing.T) {
		p, err := NewReminderPeriod("2s")
		require.NoError(t, err)
		expect := ReminderPeriod{
			value:   "2s",
			period:  2 * time.Second,
			repeats: -1,
		}
		assert.Truef(t, reflect.DeepEqual(p, expect), "Got: '%#v' Expected: '%#v'", p, expect)
	})

	t.Run("interval in ISO8601 format", func(t *testing.T) {
		p, err := NewReminderPeriod("P2WT1M")
		require.NoError(t, err)
		expect := ReminderPeriod{
			value:   "P2WT1M",
			days:    14,
			period:  time.Minute,
			repeats: -1,
		}
		assert.Truef(t, reflect.DeepEqual(p, expect), "Got: '%#v' Expected: '%#v'", p, expect)
	})

	t.Run("interval in ISO8601 format with repeats", func(t *testing.T) {
		p, err := NewReminderPeriod("R3/P2WT1M")
		require.NoError(t, err)
		expect := ReminderPeriod{
			value:   "R3/P2WT1M",
			days:    14,
			period:  time.Minute,
			repeats: 3,
		}
		assert.Truef(t, reflect.DeepEqual(p, expect), "Got: '%#v' Expected: '%#v'", p, expect)
	})

	t.Run("repeats only", func(t *testing.T) {
		p, err := NewReminderPeriod("R2")
		require.NoError(t, err)
		expect := ReminderPeriod{
			value:   "R2",
			repeats: 2,
		}
		assert.Truef(t, reflect.DeepEqual(p, expect), "Got: '%#v' Expected: '%#v'", p, expect)
	})

	t.Run("invalid interval", func(t *testing.T) {
		_, err := NewReminderPeriod("invalid")
		require.Error(t, err)
	})

	t.Run("invalid with zero repeats", func(t *testing.T) {
		_, err := NewReminderPeriod("R0")
		require.Error(t, err)
	})
}

func TestReminderPeriodJSON(t *testing.T) {
	jsonEqual := func(t *testing.T, p ReminderPeriod, wantJSON string) {
		// Marshal
		got, err := json.Marshal(p)
		require.NoError(t, err)

		// Compact the JSON before checking for equality
		out := &bytes.Buffer{}
		err = json.Compact(out, got)
		require.NoError(t, err)
		assert.Equal(t, wantJSON, out.String())

		// Unmarshal
		dec := ReminderPeriod{}
		err = json.Unmarshal(got, &dec)
		require.NoError(t, err)
		assert.True(t, reflect.DeepEqual(dec, p), "Got: `%#v`. Expected: `%#v`", dec, p)
	}

	t.Run("interval in Go format", func(t *testing.T) {
		p, err := NewReminderPeriod("2s")
		require.NoError(t, err)
		jsonEqual(t, p, `"2s"`)
	})

	t.Run("interval in ISO8601 format", func(t *testing.T) {
		p, err := NewReminderPeriod("P2WT1M")
		require.NoError(t, err)
		jsonEqual(t, p, `"P2WT1M"`)
	})

	t.Run("interval in ISO8601 format with repeats", func(t *testing.T) {
		p, err := NewReminderPeriod("R3/P2WT1M")
		require.NoError(t, err)
		jsonEqual(t, p, `"R3/P2WT1M"`)
	})

	t.Run("no JSON value", func(t *testing.T) {
		expect := ReminderPeriod{
			value:   "",
			repeats: -1,
		}
		dec := ReminderPeriod{}
		err := dec.UnmarshalJSON([]byte{}) // Note this is an empty value
		require.NoError(t, err)
		assert.True(t, reflect.DeepEqual(dec, expect), "Got: `%#v`. Expected: `%#v`", dec, expect)
	})

	t.Run("empty JSON values", func(t *testing.T) {
		tests := map[string]string{
			"empty string": `""`,
			"null":         "null",
			"empty array":  "[]",
			"empty object": "{}",
		}

		expect := ReminderPeriod{
			value:   "",
			repeats: -1,
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				dec := ReminderPeriod{}
				err := json.Unmarshal([]byte(tt), &dec)
				require.NoError(t, err)
				assert.True(t, reflect.DeepEqual(dec, expect), "Got: `%#v`. Expected: `%#v`", dec, expect)
			})
		}
	})

	t.Run("not a JSON value", func(t *testing.T) {
		dec := ReminderPeriod{}
		// This is just "foo" and not as a JSON string, so it's invalid
		err := json.Unmarshal([]byte("foo"), &dec)
		require.Error(t, err)
	})

	t.Run("invalid JSON string", func(t *testing.T) {
		dec := ReminderPeriod{}
		err := json.Unmarshal([]byte(`"invalid"`), &dec)
		require.Error(t, err)
	})
}
