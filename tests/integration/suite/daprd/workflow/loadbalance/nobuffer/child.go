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
	suite.Register(new(child))
}

// child runs nobuffer.NoBuffer with a child workflow as the middle step.
type child struct {
	nb *nobuffer.NoBuffer
}

func (c *child) Setup(t *testing.T) []framework.Option {
	c.nb = nobuffer.New(t, nobuffer.Options{
		ID:     "earlychildnobuffer",
		Middle: nobuffer.StepChild,
	})
	return []framework.Option{
		framework.WithProcesses(c.nb),
	}
}

func (c *child) Run(t *testing.T, ctx context.Context) {
	c.nb.Execute(t, ctx)
}
