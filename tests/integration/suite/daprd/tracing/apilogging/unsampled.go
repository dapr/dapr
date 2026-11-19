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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/framework/tracing"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unsampled))
}

// unsampled asserts that no trace IDs are logged for a span that is never
// exported. The sampler is ParentBased, so an inbound traceparent with the
// sampled flag unset is dropped even at a sampling rate of 1.
type unsampled struct {
	daprd     *daprd.Daprd
	collector *otel.Collector
	logline   *logline.LogLine
}

func (u *unsampled) Setup(t *testing.T) []framework.Option {
	u.collector = otel.New(t)
	u.logline = logline.New(t, logline.WithCaptureAll())

	u.daprd = daprd.New(t,
		u.collector.GRPCDaprdConfiguration(t),
		daprd.WithExecOptions(
			exec.WithStdout(u.logline.Stdout()),
			exec.WithStderr(u.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.logline, u.collector, u.daprd),
	}
}

func (u *unsampled) Run(t *testing.T, ctx context.Context) {
	u.collector.WaitUntilRunning(t, ctx)
	u.daprd.WaitUntilRunning(t, ctx)

	traceID, traceparent := tracing.Traceparent(t, false)
	callAPIs(t, ctx, u.daprd, traceparent)

	assertNoTraceFields(t, u.logline)
	assert.Empty(t, u.collector.TraceSpans(traceID), "the span should not have been exported")
}
