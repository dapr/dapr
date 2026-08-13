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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/api/universal"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(forcegrpc))
}

// forcegrpc verifies that the gRPC Shutdown RPC with metadata
// "dapr-force-shutdown: true" causes daprd to exit immediately with a
// non-zero exit code, bypassing graceful shutdown.
type forcegrpc struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (f *forcegrpc) Setup(t *testing.T) []framework.Option {
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

func (f *forcegrpc) Run(t *testing.T, ctx context.Context) {
	f.daprd.Run(t, ctx)
	f.daprd.WaitUntilRunning(t, ctx)

	mdCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(universal.ForceShutdownMetadataKey, "true"))
	_, err := f.daprd.GRPCClient(t, ctx).Shutdown(mdCtx, &rtv1.ShutdownRequest{})
	require.NoError(t, err)

	f.logline.EventuallyFoundAll(t)
	f.daprd.WaitUntilExit(t, 10*time.Second)
}
