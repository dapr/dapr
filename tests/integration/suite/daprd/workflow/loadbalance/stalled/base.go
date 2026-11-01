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
	suite.Register(new(base))
}

// base verifies a workflow stalls and recovers in a clustered deployment with
// one worker client on each daprd.
type base struct {
	stalled *stalled.Permutation
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.stalled = stalled.NewPermutation(t, stalled.PermutationOptions{
		Daprds:   2,
		V1:       []int{0, 1},
		V2:       []int{0, 1},
		Recovery: []int{0, 1},
	})

	return []framework.Option{
		framework.WithProcesses(b.stalled),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.stalled.Execute(t, ctx)
}
