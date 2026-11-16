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

package nobuffer

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/nobuffer"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(childcycle))
}

// childcycle is child, but it holds the child's terminal commit, and with it
// the child's turn lock, until the parent's retried turn is blocked
// re-creating the child. The child then sends its completion to the parent
// holding its own lock while the parent waits for the child's: the workflow
// completes only once the child's send gives up and its retry reminder
// delivers.
type childcycle struct {
	nb *nobuffer.NoBuffer
}

func (c *childcycle) Setup(t *testing.T) []framework.Option {
	c.nb = nobuffer.New(t, nobuffer.Options{
		ID:        "earlychildcycle",
		Middle:    nobuffer.StepChild,
		HoldChild: true,
	})
	return []framework.Option{
		framework.WithProcesses(c.nb),
	}
}

func (c *childcycle) Run(t *testing.T, ctx context.Context) {
	c.nb.Execute(t, ctx)
}
