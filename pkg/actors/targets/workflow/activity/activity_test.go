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

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

func Test_workflowID(t *testing.T) {
	for actorID, want := range map[string]string{
		common.ActivityActorID("abc", 5):        "abc",
		common.ActivityActorID("collide::0", 3): "collide::0",
		common.ActivityActorID("a::b::c", 0):    "a::b::c",
	} {
		got, err := (&activity{actorID: actorID}).workflowID()
		require.NoError(t, err)
		assert.Equal(t, want, got, actorID)
	}

	_, err := (&activity{actorID: "noseparator"}).workflowID()
	require.Error(t, err)
}
