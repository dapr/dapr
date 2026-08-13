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

package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_siblingRendezvousKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		actorID string
		want    string
	}{
		"pre-upgrade activity key": {
			actorID: "abc/5",
			want:    "abc::5",
		},
		"current activity key": {
			actorID: "abc::5",
			want:    "abc/5",
		},
		"round trip is identity": {
			actorID: siblingRendezvousKey("abc::5"),
			want:    "abc::5",
		},
		"workflow instance ID": {
			actorID: "abc",
			want:    "",
		},
		"workflow instance ID with non-numeric colon suffix": {
			actorID: "abc::def",
			want:    "",
		},
		"instance ID containing double colon in activity key": {
			actorID: "a::b::7",
			want:    "a::b/7",
		},
		"negative task ID": {
			actorID: "abc::-1",
			want:    "abc/-1",
		},
		"empty": {
			actorID: "",
			want:    "",
		},
		"leading separator only": {
			actorID: "::5",
			want:    "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, siblingRendezvousKey(test.actorID))
		})
	}
}
