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

package validation

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reserved))
}

// reserved ensures that daprd refuses to start when spec.actors.stateStores
// binds an internal actor type (dapr.internal.*), such as the workflow
// actors, whose state must stay in the default actor state store.
type reserved struct {
	logline *logline.LogLine
}

func (r *reserved) Setup(t *testing.T) []framework.Option {
	r.logline = logline.New(t, logline.WithStdoutLineContains(
		`actor type \"dapr.internal.default.myapp.workflow\" is reserved for Dapr internal actors`,
	))

	daprd := daprd.New(t,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-a
      actorTypes: ["dapr.internal.default.myapp.workflow"]
`),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(r.logline),
	)

	return []framework.Option{
		framework.WithProcesses(r.logline, daprd),
	}
}

func (r *reserved) Run(t *testing.T, ctx context.Context) {
	// daprd.WithExit1 asserts at cleanup that daprd exited with code 1.
	r.logline.EventuallyFoundAll(t)
}
