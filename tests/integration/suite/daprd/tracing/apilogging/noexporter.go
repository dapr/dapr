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

package apilogging

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/tracing"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noExporter))
}

// noExporter asserts that no trace IDs are logged when daprd has no tracing
// Configuration. The tracing middleware still runs, because an empty sampling
// rate falls back to a non-zero default, but no exporter is registered so the
// tracer is a no-op and the span context in the request is the caller's.
type noExporter struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (n *noExporter) Setup(t *testing.T) []framework.Option {
	n.logline = logline.New(t, logline.WithCaptureAll())

	n.daprd = daprd.New(t,
		daprd.WithExecOptions(
			exec.WithStdout(n.logline.Stdout()),
			exec.WithStderr(n.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.daprd),
	}
}

func (n *noExporter) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	_, traceparent := tracing.Traceparent(t, true)
	callAPIs(t, ctx, n.daprd, traceparent)

	assertNoTraceFields(t, n.logline)
}
