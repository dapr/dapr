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

package chaos

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow/terminalcost"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(terminalwakeup))
}

// terminalwakeup runs terminalcost.TerminalCost: what a completed workflow
// costs to poke must not grow with its history.
type terminalwakeup struct {
	tc *terminalcost.TerminalCost
}

func (w *terminalwakeup) Setup(t *testing.T) []framework.Option {
	w.tc = terminalcost.New(t)
	return []framework.Option{
		framework.WithProcesses(w.tc),
	}
}

func (w *terminalwakeup) Run(t *testing.T, ctx context.Context) {
	w.tc.Execute(t, ctx)
}
