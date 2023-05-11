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

package daprd

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/base"
)

// options contains the options for running Daprd in integration tests.
type options struct {
	baseOpts []base.Option

	appID                   string
	appPort                 int
	grpcPort                int
	httpPort                int
	internalGRPCPort        int
	publicPort              int
	metricsPort             int
	profilePort             int
	appHealthCheck          bool
	appHealthCheckPath      string
	appHealthProbeInterval  int
	appHealthProbeThreshold int
}

// Option is a function that configures the DaprdOptions.
type Option func(*options)

type Daprd struct {
	base process.Interface

	AppID            string
	AppPort          int
	GRPCPort         int
	HTTPPort         int
	InternalGRPCPort int
	PublicPort       int
	MetricsPort      int
	ProfilePort      int
}

func New(t *testing.T, fopts ...Option) *Daprd {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	appListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, appListener.Close())
	})

	go func() {
		for {
			conn, err := appListener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			conn.Close()
		}
	}()

	opts := options{
		appID:            uid.String(),
		appPort:          appListener.Addr().(*net.TCPAddr).Port,
		grpcPort:         process.FreePort(t),
		httpPort:         process.FreePort(t),
		internalGRPCPort: process.FreePort(t),
		publicPort:       process.FreePort(t),
		metricsPort:      process.FreePort(t),
		profilePort:      process.FreePort(t),
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + "debug",
		"--app-id=" + opts.appID,
		"--app-port=" + strconv.Itoa(opts.appPort),
		"--dapr-grpc-port=" + strconv.Itoa(opts.grpcPort),
		"--dapr-http-port=" + strconv.Itoa(opts.httpPort),
		"--dapr-internal-grpc-port=" + strconv.Itoa(opts.internalGRPCPort),
		"--dapr-public-port=" + strconv.Itoa(opts.publicPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--profile-port=" + strconv.Itoa(opts.profilePort),
		"--enable-app-health-check=" + strconv.FormatBool(opts.appHealthCheck),
		"--app-health-probe-interval=" + strconv.Itoa(opts.appHealthProbeInterval),
		"--app-health-threshold=" + strconv.Itoa(opts.appHealthProbeThreshold),
	}
	if opts.appHealthCheckPath != "" {
		args = append(args, "--app-health-check-path="+opts.appHealthCheckPath)
	}

	return &Daprd{
		base:             base.New(t, os.Getenv("DAPR_INTEGRATION_DAPRD_PATH"), args, opts.baseOpts...),
		AppID:            opts.appID,
		AppPort:          opts.appPort,
		GRPCPort:         opts.grpcPort,
		HTTPPort:         opts.httpPort,
		InternalGRPCPort: opts.internalGRPCPort,
		PublicPort:       opts.publicPort,
		MetricsPort:      opts.metricsPort,
		ProfilePort:      opts.profilePort,
	}
}

func (d *Daprd) Run(t *testing.T, ctx context.Context) {
	d.base.Run(t, ctx)
}

func (d *Daprd) Cleanup(t *testing.T) {
	d.base.Cleanup(t)
}
