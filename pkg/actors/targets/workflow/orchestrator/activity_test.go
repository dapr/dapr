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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

func Test_buildActivityActorID(t *testing.T) {
	t.Parallel()

	// The activity actor ID must stay identical to the executor rendezvous
	// key used by ClusterTasksBackend: under WorkflowsClusteredDeployment
	// placement co-locates the rendezvous actor with the activity actor
	// through ID equality. It must also stay a valid scheduler job name
	// ('/', '\', '#' and '?' are forbidden), as it is embedded in the
	// run-activity reminder job name.
	assert.Equal(t, "abc::5", buildActivityActorID("abc", 5))
	assert.Equal(t, common.ActivityActorID("abc", 5), buildActivityActorID("abc", 5))
}
