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
	suite.Register(new(workersubset))
}

// workersubset stalls and recovers on a three daprd cluster where only daprd
// 0 ever hosts a worker.
type workersubset struct {
	stalled *stalled.Permutation
}

func (w *workersubset) Setup(t *testing.T) []framework.Option {
	w.stalled = stalled.NewPermutation(t, stalled.PermutationOptions{
		Daprds:   3,
		V1:       []int{0},
		V2:       []int{0},
		Recovery: []int{0},
	})

	return []framework.Option{
		framework.WithProcesses(w.stalled),
	}
}

func (w *workersubset) Run(t *testing.T, ctx context.Context) {
	w.stalled.Execute(t, ctx)
}
