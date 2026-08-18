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

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(forcehttp))
}

// forcehttp verifies that POST /v1.0/shutdown with header
// "Dapr-Force-Shutdown: true" causes daprd to exit immediately with a
// non-zero exit code, bypassing graceful shutdown.
type forcehttp struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (f *forcehttp) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	f.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Force shutdown requested, exiting immediately without graceful shutdown",
		),
	)

	f.daprd = daprd.New(t,
		daprd.WithExit1(),
		daprd.WithLogLineStdout(f.logline),
	)

	return []framework.Option{
		framework.WithProcesses(f.logline),
	}
}

func (f *forcehttp) Run(t *testing.T, ctx context.Context) {
	f.daprd.Run(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	f.daprd.HTTPPost(t, ctx, "/v1.0/shutdown", nil, http.StatusNoContent,
		universal.ForceShutdownMetadataKey, "true",
	)

	f.logline.EventuallyFoundAll(t)
	f.daprd.WaitUntilExit(t, 10*time.Second)
}
