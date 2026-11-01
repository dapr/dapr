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

package ports

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalgrpcsighup))
}

// internalgrpcsighup verifies that the internal gRPC port stays bound while
// daprd restarts its runtime on SIGHUP, so no other socket can take it.
type internalgrpcsighup struct {
	daprd *procdaprd.Daprd
	log   *log.Log
}

func (i *internalgrpcsighup) Setup(t *testing.T) []framework.Option {
	i.log = log.New()
	i.daprd = procdaprd.New(t,
		procdaprd.WithExecOptions(exec.WithStdout(i.log), exec.WithStderr(i.log)),
	)

	return []framework.Option{
		framework.WithProcesses(i.daprd),
	}
}

func (i *internalgrpcsighup) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	addr := i.daprd.InternalGRPCAddress()

	ln, err := net.Listen("tcp", addr)
	if err == nil {
		require.NoError(t, ln.Close())
	}
	require.Error(t, err, "internal gRPC port %s was free before SIGHUP", addr)

	stolen := make(chan struct{})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if sl, serr := net.Listen("tcp", addr); serr == nil {
				// Release it at once so daprd can still bind it.
				sl.Close()
				close(stolen)
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	})
	defer func() {
		close(stop)
		wg.Wait()
	}()

	i.log.Reset()
	i.daprd.SignalHUP(t)

	timeout := time.After(20 * time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	var margin <-chan time.Time
loop:
	for {
		select {
		case <-stolen:
			require.FailNowf(t, "internal gRPC port was free during the SIGHUP restart",
				"bound %s while daprd was restarting its runtime", addr)
		case <-timeout:
			require.FailNow(t, "daprd did not start its internal gRPC server again after SIGHUP")
		case <-margin:
			break loop
		case <-poll.C:
			if margin == nil && i.log.Contains("Internal gRPC server is running") {
				margin = time.After(time.Second)
			}
		}
	}

	i.daprd.WaitUntilRunning(t, ctx)
}
