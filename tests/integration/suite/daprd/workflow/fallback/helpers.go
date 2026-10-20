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

package fallback

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
)

// withDamping sets the damping period after which a fallback host gives the
// workflow actor types up to a daprd with a worker, in place of the default.
func withDamping(t *testing.T, d time.Duration) daprd.Option {
	return daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_FALLBACK_DAMPING", d.String()))
}

// hostsWorkflows returns whether the daprd at index hosts the workflow actor
// type, as a fallback host or with a worker. A failed metadata read fails c
// and returns false.
func hostsWorkflows(c assert.TestingT, ctx context.Context, wf *workflow.Workflow, index int) bool {
	rt := wf.DaprN(index).GetMetaActorRuntime(c, ctx)
	if rt == nil {
		return false
	}
	for _, a := range rt.ActiveActors {
		if a.Type == wf.WorkflowActorType(index) {
			return true
		}
	}
	return false
}

// readErrors records whether a read failed, for a background poll which
// must not fail the test on a read cut short by its own end.
type readErrors struct{ failed bool }

func (r *readErrors) Errorf(string, ...any) { r.failed = true }
