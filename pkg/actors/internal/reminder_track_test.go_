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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/ptr"
)

func TestReminderTrackJSON(t *testing.T) {
	time1, _ := time.Parse(time.RFC3339, "2023-03-07T18:29:04Z")

	type fields struct {
		LastFiredTime  time.Time
		RepetitionLeft int
		Etag           *string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name:   "basic test",
			fields: fields{LastFiredTime: time1, RepetitionLeft: 2},
			want:   `{"lastFiredTime":"2023-03-07T18:29:04Z","repetitionLeft":2}`,
		},
		{
			name:   "has etag",
			fields: fields{LastFiredTime: time1, RepetitionLeft: 2, Etag: ptr.Of("foo")},
			want:   `{"lastFiredTime":"2023-03-07T18:29:04Z","repetitionLeft":2,"Etag":"foo"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ReminderTrack{
				LastFiredTime:  tt.fields.LastFiredTime,
				RepetitionLeft: tt.fields.RepetitionLeft,
				Etag:           tt.fields.Etag,
			}

			// Marshal
			got, err := json.Marshal(&r)
			require.NoError(t, err)

			// Compact the JSON before checking for equality
			got = compactJSON(t, got)
			assert.Equal(t, tt.want, string(got))

			// Unmarshal
			dec := ReminderTrack{}
			err = json.Unmarshal(got, &dec)
			require.NoError(t, err)
			assert.True(t, reflect.DeepEqual(dec, r), "Got: `%#v`. Expected: `%#v`", dec, r)
		})
	}
}
