/*
Copyright 2025 The Dapr Authors
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

package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_workflowID freezes every activity actor ID shape a released version of Dapr has
// written. The table is append-only: a row may only be removed once the
// release that wrote it is outside the version skew window.
func Test_workflowID(t *testing.T) {
	tests := []struct {
		actorID string
		taskID  int32
		want    string
	}{
		// v1.15.0 to v1.18.x: <instanceID>::<taskID>::<generation>
		{"wf::5::0", 5, "wf"},
		{"wf::0::0", 0, "wf"},
		{"colon::id::4::1", 4, "colon::id"},
		{"trailing::::5::0", 5, "trailing::"},
		{"job::3::7::0", 7, "job::3"},
		{"wf::2::184467440737095516", 2, "wf"},

		// v1.19 and later: <instanceID>::<taskID>
		{"wf::5", 5, "wf"},
		{"a::b::5", 5, "a::b"},
		{"a::b::c::5", 5, "a::b::c"},
		{"job::7::3", 3, "job::7"},
		{"trailing::::5", 5, "trailing::"},

		// Documented residual: a v1.19 ID whose instance ends in "::<taskID>"
		// is indistinguishable from a legacy ID and takes the legacy reading.
		{"job::3::3", 3, "job"},
	}

	for _, tt := range tests {
		got, err := (&activity{actorID: tt.actorID}).workflowID(tt.taskID)
		require.NoError(t, err, tt.actorID)
		assert.Equal(t, tt.want, got, tt.actorID)
	}

	_, err := (&activity{actorID: "noseparator"}).workflowID(0)
	require.Error(t, err)
}
