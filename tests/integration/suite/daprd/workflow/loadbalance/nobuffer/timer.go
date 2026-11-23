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
	suite.Register(new(timer))
}

// timer runs nobuffer.NoBuffer with a zero-length timer as the middle step.
type timer struct {
	nb *nobuffer.NoBuffer
}

func (m *timer) Setup(t *testing.T) []framework.Option {
	m.nb = nobuffer.New(t, nobuffer.Options{
		ID:     "earlytimernobuffer",
		Middle: nobuffer.StepTimer,
	})
	return []framework.Option{
		framework.WithProcesses(m.nb),
	}
}

func (m *timer) Run(t *testing.T, ctx context.Context) {
	m.nb.Execute(t, ctx)
}
