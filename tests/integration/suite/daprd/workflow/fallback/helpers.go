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
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
)

// fallbackSettle is the settle period the tests give their daprds in place
// of the default, so a daprd without a worker turns into a fallback host soon
// after the placement tables show that no daprd of the app has one.
const fallbackSettle = time.Second

// doneOutput is what the tests' workflows return once they complete.
const doneOutput = "done"

// withSettle gives the daprd at index the tests' settle period.
func withSettle(t *testing.T, index int) workflow.Option {
	return workflow.WithDaprdOptions(index, daprd.WithWorkflowFallbackSettle(t, fallbackSettle))
}

// newPair returns two daprds of one app with staged operations and the given
// fallback settle period, the second with the given fallback damping period,
// in place of the defaults, and opts applied on top.
func newPair(t *testing.T, damping, settle time.Duration, opts ...workflow.Option) *workflow.Workflow {
	t.Helper()

	appID := uuid.NewString()
	return workflow.New(t, append([]workflow.Option{
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, settle), daprd.WithWorkflowFallbackDamping(t, damping)),
	}, opts...)...)
}
