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
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestReminderProperties(t *testing.T) {
	t.Parallel()

	time1, _ := time.Parse(time.RFC3339, "2023-03-07T18:29:04Z")

	r := Reminder{
		ActorID:        "id",
		ActorType:      "type",
		Name:           "name",
		RegisteredTime: time1,
	}

	t.Run("ActorKey", func(t *testing.T) {
		require.Equal(t, "type||id", r.ActorKey())
	})

	t.Run("Key", func(t *testing.T) {
		require.Equal(t, "type||id||name", r.Key())
	})

	t.Run("NextTick", func(t *testing.T) {
		nextTick, active := r.NextTick()
		require.Equal(t, time1, nextTick)
		require.True(t, active)
	})

	t.Run("without repeats", func(t *testing.T) {
		require.False(t, r.HasRepeats())
		require.Equal(t, 0, r.RepeatsLeft())
		require.Equal(t, 0, r.Period.repeats)
		require.True(t, r.TickExecuted()) // It's done, no more repeats
		require.Equal(t, 0, r.Period.repeats)
	})

	// Update the object to add a period
	var err error
	r.Period, err = NewReminderPeriod("2s")
	require.NoError(t, err)

	t.Run("with unlimited repeats", func(t *testing.T) {
		require.True(t, r.HasRepeats())
		require.Equal(t, -1, r.RepeatsLeft())
		require.Equal(t, -1, r.Period.repeats)

		nextTick, active := r.NextTick()
		require.Equal(t, time1, nextTick)
		require.True(t, active)

		// Execute the tick
		require.False(t, r.TickExecuted()) // Will repeat
		require.Equal(t, -1, r.Period.repeats)

		nextTick, active = r.NextTick()
		require.Equal(t, time1.Add(2*time.Second), nextTick)
		require.True(t, active)
	})

	// Update the object to add limited repeats
	r.RegisteredTime = time1
	r.Period, err = NewReminderPeriod("R4/PT2S")
	require.NoError(t, err)

	t.Run("with limited repeats", func(t *testing.T) {
		require.True(t, r.HasRepeats())

		// Execute the tick 4 times
		for i := 4; i > 0; i-- {
			require.Equal(t, i, r.RepeatsLeft())
			require.Equal(t, i, r.Period.repeats)
			nextTick, active := r.NextTick()
			require.Equal(t, time1.Add(2*time.Second*time.Duration(4-i)), nextTick)
			require.True(t, active)

			if i == 1 {
				require.True(t, r.TickExecuted()) // Done, won't repeat
			} else {
				require.False(t, r.TickExecuted()) // Will repeat
			}
		}
	})

	// Update the object to set an expiration
	r.RegisteredTime = time1
	r.ExpirationTime = time1.Add(6 * time.Second)
	r.Period, err = NewReminderPeriod("2s")
	require.NoError(t, err)

	t.Run("with expiration time", func(t *testing.T) {
		require.True(t, r.HasRepeats())
		require.Equal(t, -1, r.RepeatsLeft())
		require.Equal(t, -1, r.Period.repeats)

		for i := range 4 {
			nextTick, active := r.NextTick()
			require.Equal(t, time1.Add((2*time.Second)*time.Duration(i)), nextTick)

			if i == 3 {
				require.False(t, active)
			} else {
				require.True(t, active)
			}

			require.False(t, r.TickExecuted())
		}
	})
}

func TestReminderJSON(t *testing.T) {
	t.Parallel()

	time1, _ := time.Parse(time.RFC3339, "2023-03-07T18:29:04Z")
	time2, _ := time.Parse(time.RFC3339, "2023-02-01T11:02:01Z")

	type fields struct {
		ActorID        string
		ActorType      string
		Name           string
		Data           any
		Period         string
		RegisteredTime time.Time
		DueTime        string
		ExpirationTime time.Time
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{name: "base test", fields: fields{ActorID: "id", ActorType: "type", Name: "name"}, want: `{"actorID":"id","actorType":"type","name":"name"}`},
		{name: "with data", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Data: "hi"}, want: `{"data":"hi","actorID":"id","actorType":"type","name":"name"}`},
		{name: "with period", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s"}, want: `{"period":"2s","actorID":"id","actorType":"type","name":"name"}`},
		{name: "with due time", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s", DueTime: "2m", RegisteredTime: time1}, want: `{"registeredTime":"2023-03-07T18:29:04Z","period":"2s","actorID":"id","actorType":"type","name":"name","dueTime":"2m"}`},
		{name: "with expiration time", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s", ExpirationTime: time2}, want: `{"expirationTime":"2023-02-01T11:02:01Z","period":"2s","actorID":"id","actorType":"type","name":"name"}`},
		{name: "with data as JSON object", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Data: json.RawMessage(`{  "foo": [ 12, 4 ] } `)}, want: `{"data":{"foo":[12,4]},"actorID":"id","actorType":"type","name":"name"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Reminder{
				ActorID:        tt.fields.ActorID,
				ActorType:      tt.fields.ActorType,
				Name:           tt.fields.Name,
				RegisteredTime: tt.fields.RegisteredTime,
				DueTime:        tt.fields.DueTime,
				ExpirationTime: tt.fields.ExpirationTime,
			}
			if tt.fields.Data != nil {
				bb, err := json.Marshal(tt.fields.Data)
				require.NoError(t, err)
				r.Data, err = anypb.New(wrapperspb.Bytes(bb))
				require.NoError(t, err)
			}
			var err error
			r.Period, err = NewReminderPeriod(tt.fields.Period)
			require.NoError(t, err)

			// Marshal
			got, err := json.Marshal(&r)
			require.NoError(t, err)

			// Compact the JSON before checking for equality
			got = compactJSON(t, got)
			assert.Equal(t, tt.want, string(got))

			// Unmarshal
			dec := Reminder{}
			err = json.Unmarshal(got, &dec)
			require.NoError(t, err)
			assert.True(t, reflect.DeepEqual(dec, r), "Got: `%#v`. Expected: `%#v`", dec, r)
		})
	}

	t.Run("slice", func(t *testing.T) {
		const payload = `[{"data":{"foo":[12,4]},"actorID":"id","actorType":"type","name":"name"},{"registeredTime":"2023-03-07T18:29:04Z","period":"2s","actorID":"id","actorType":"type","name":"name","dueTime":"2m"}]`

		dec := []Reminder{}
		err := json.Unmarshal([]byte(payload), &dec)
		require.NoError(t, err)

		// Marshal
		enc, err := json.Marshal(dec)
		require.NoError(t, err)
		require.JSONEq(t, payload, string(enc))
	})

	t.Run("failed to unmarshal", func(t *testing.T) {
		t.Run("cannot decode RegisteredTime", func(t *testing.T) {
			const enc = `{"registeredTime":"invalid date","period":"2s","actorID":"id","actorType":"type","name":"name","dueTime":"2m"}`
			err := json.Unmarshal([]byte(enc), &Reminder{})
			require.Error(t, err)
			require.ErrorContains(t, err, "failed to parse RegisteredTime")
		})

		t.Run("cannot decode ExpirationTime", func(t *testing.T) {
			const enc = `{"expirationTime":"invalid date","period":"2s","actorID":"id","actorType":"type","name":"name","dueTime":"2m"}`
			err := json.Unmarshal([]byte(enc), &Reminder{})
			require.Error(t, err)
			require.ErrorContains(t, err, "failed to parse ExpirationTime")
		})
	})
}

func TestReminderString(t *testing.T) {
	t.Parallel()

	time1, _ := time.Parse(time.RFC3339, "2023-03-07T18:29:04Z")
	time2, _ := time.Parse(time.RFC3339, "2023-02-01T11:02:01Z")

	type fields struct {
		ActorID        string
		ActorType      string
		Name           string
		Data           json.RawMessage
		Period         string
		DueTime        time.Time
		DueTimeReq     string
		ExpirationTime time.Time
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{name: "base test", fields: fields{ActorID: "id", ActorType: "type", Name: "name"}, want: `name='type||id||name' hasData=false period=nil dueTime=nil expirationTime=nil`},
		{name: "with data", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Data: json.RawMessage(`"hi"`)}, want: `name='type||id||name' hasData=true period=nil dueTime=nil expirationTime=nil`},
		{name: "with period", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s"}, want: `name='type||id||name' hasData=false period='2s' dueTime=nil expirationTime=nil`},
		{name: "with due time", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s", DueTimeReq: "2m", DueTime: time1}, want: `name='type||id||name' hasData=false period='2s' dueTime='2023-03-07T18:29:04Z' expirationTime=nil`},
		{name: "with expiration time", fields: fields{ActorID: "id", ActorType: "type", Name: "name", Period: "2s", ExpirationTime: time2}, want: `name='type||id||name' hasData=false period='2s' dueTime=nil expirationTime='2023-02-01T11:02:01Z'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data *anypb.Any
			var err error
			if len(tt.fields.Data) > 0 {
				data, err = anypb.New(wrapperspb.Bytes(tt.fields.Data))
				require.NoError(t, err)
			}
			r := Reminder{
				ActorID:        tt.fields.ActorID,
				ActorType:      tt.fields.ActorType,
				Name:           tt.fields.Name,
				RegisteredTime: tt.fields.DueTime,
				DueTime:        tt.fields.DueTimeReq,
				ExpirationTime: tt.fields.ExpirationTime,
				Data:           data,
			}
			r.Period, err = NewReminderPeriod(tt.fields.Period)
			require.NoError(t, err)

			// Encode to string
			got := r.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func compactJSON(t *testing.T, data []byte) []byte {
	out := &bytes.Buffer{}
	err := json.Compact(out, data)
	require.NoError(t, err)
	return out.Bytes()
}
