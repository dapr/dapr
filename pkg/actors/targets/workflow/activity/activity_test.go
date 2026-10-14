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

// The table freezes the actor ID shape every released version writes. Rows
// are literals so no builder can drift it.
func Test_workflowID(t *testing.T) {
	for _, tc := range []struct{ actorID, want string }{
		{"wf::5::0", "wf"},
		{"wf::0::0", "wf"},
		{"colon::id::4::1", "colon::id"},
		{"trailing::::5::0", "trailing::"},
		{"job::3::7::0", "job::3"},
		{"job::3::3::0", "job::3"},
		{"collide::0::0::0", "collide::0"},
		{"wf::2::184467440737095516", "wf"},
	} {
		got, err := (&activity{actorID: tc.actorID}).workflowID()
		require.NoError(t, err, tc.actorID)
		assert.Equal(t, tc.want, got, tc.actorID)
	}

	for _, actorID := range []string{"noseparator", "wf::5"} {
		_, err := (&activity{actorID: actorID}).workflowID()
		require.Error(t, err, actorID)
	}
}
