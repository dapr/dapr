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

package signing

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nomtlserror))
}

// nomtlserror verifies that when the WorkflowSignState feature flag is
// explicitly enabled but mTLS is not configured (no sentry), daprd fails
// to start with a clear error message.
type nomtlserror struct {
	logline *logline.LogLine
}

func (n *nomtlserror) Setup(t *testing.T) []framework.Option {
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	place := placement.New(t)
	sched := scheduler.New(t)

	n.logline = logline.New(t,
		logline.WithStdoutLineContains("WorkflowSignState feature flag is enabled but mTLS is not configured"),
	)

	daprd := daprd.New(t,
		// No WithSentry — mTLS is NOT configured.
		daprd.WithPlacement(place),
		daprd.WithScheduler(sched),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sign-on
spec:
  features:
  - name: WorkflowSignState
    enabled: true
`),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(n.logline),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, db, place, sched, daprd),
	}
}

func (n *nomtlserror) Run(t *testing.T, _ context.Context) {
	n.logline.EventuallyFoundAll(t)
}
