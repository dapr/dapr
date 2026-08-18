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

package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(gracefulhttp))
}

// gracefulhttp verifies that POST /v1.0/shutdown causes daprd to exit cleanly
// (code 0). Regression test for the shutdown API previously being a no-op.
type gracefulhttp struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (g *gracefulhttp) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	g.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Shutdown requested via API",
			"Daprd shutdown gracefully",
		),
	)

	g.daprd = daprd.New(t,
		daprd.WithLogLineStdout(g.logline),
	)

	return []framework.Option{
		framework.WithProcesses(g.logline),
	}
}

func (g *gracefulhttp) Run(t *testing.T, ctx context.Context) {
	g.daprd.Run(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	g.daprd.HTTPPost(t, ctx, "/v1.0/shutdown", nil, http.StatusNoContent)

	g.logline.EventuallyFoundAll(t)
	g.daprd.WaitUntilExit(t, 15*time.Second)
}
