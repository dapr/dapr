/*
Copyright 2023 The Dapr Authors
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
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprd))
}

// daprd tests that the ports are available when daprd is running.
type daprd struct {
	proc *procdaprd.Daprd
}

func (d *daprd) Setup(t *testing.T) []framework.Option {
	d.proc = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(d.proc),
	}
}

func (d *daprd) Run(t *testing.T, ctx context.Context) {
	dialer := net.Dialer{Timeout: time.Second * 5}
	for name, port := range map[string]int{
		"app":           d.proc.AppPort(),
		"grpc":          d.proc.GRPCPort(),
		"http":          d.proc.HTTPPort(),
		"metrics":       d.proc.MetricsPort(),
		"internal-grpc": d.proc.InternalGRPCPort(),
		"public":        d.proc.PublicPort(),
	} {
		assert.Eventuallyf(t, func() bool {
			conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", port))
			if err != nil {
				return false
			}
			require.NoError(t, conn.Close())
			return true
		}, time.Second*5, 100*time.Millisecond, "port %s (:%d) was not available in time", name, port)
	}
}
