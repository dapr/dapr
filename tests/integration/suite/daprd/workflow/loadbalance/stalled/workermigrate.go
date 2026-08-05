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

package stalled

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/stalled"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(workermigrate))
}

// workermigrate stalls and recovers on a three daprd cluster where the worker
// moves to a different daprd on every reconnect.
type workermigrate struct {
	stalled *stalled.Permutation
}

func (w *workermigrate) Setup(t *testing.T) []framework.Option {
	w.stalled = stalled.NewPermutation(t, stalled.PermutationOptions{
		Daprds:   3,
		V1:       []int{0},
		V2:       []int{1},
		Recovery: []int{2},
	})

	return []framework.Option{
		framework.WithProcesses(w.stalled),
	}
}

func (w *workermigrate) Run(t *testing.T, ctx context.Context) {
	w.stalled.Execute(t, ctx)
}
