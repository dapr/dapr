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
	suite.Register(new(duplicate))
}

// duplicate ensures that daprd refuses to start when the same actor type is
// bound to two state stores in spec.actors.stateStores.
type duplicate struct {
	logline *logline.LogLine
}

func (d *duplicate) Setup(t *testing.T) []framework.Option {
	d.logline = logline.New(t, logline.WithStdoutLineContains(
		`actor type \"TransactionActor\" is bound to more than one state store: \"wallet-a\" and \"wallet-b\"`,
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
      actorTypes: ["TransactionActor"]
    - name: wallet-b
      actorTypes: ["TransactionActor"]
`),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(d.logline),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, daprd),
	}
}

func (d *duplicate) Run(t *testing.T, ctx context.Context) {
	// daprd.WithExit1 asserts at cleanup that daprd exited with code 1.
	d.logline.EventuallyFoundAll(t)
}
