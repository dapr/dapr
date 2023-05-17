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

package framework

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/freeport"
	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/kill"
)

// daprdOptions contains the options for running Daprd in integration tests.
type daprdOptions struct {
	stdout io.WriteCloser
	stderr io.WriteCloser

	binPath string
	appID   string

	appPort                 int
	grpcPort                int
	httpPort                int
	internalGRPCPort        int
	publicPort              int
	metricsPort             int
	profilePort             int
	runErrorFn              func(error)
	exitCode                int
	appHealthCheck          bool
	appHealthCheckPath      string
	appHealthProbeInterval  int
	appHealthProbeThreshold int
}

// RunDaprdOption is a function that configures the DaprdOptions.
type RunDaprdOption func(*daprdOptions)

type Command struct {
	lock sync.Mutex
	cmd  *exec.Cmd

	cmdcancel    context.CancelFunc
	runErrorFnFn func(error)
	exitCode     int
	stdoutpipe   io.WriteCloser
	stderrpipe   io.WriteCloser

	AppID            string
	AppPort          int
	GRPCPort         int
	HTTPPort         int
	InternalGRPCPort int
	PublicPort       int
	MetricsPort      int
	ProfilePort      int
}

func RunDaprd(t *testing.T, ctx context.Context, opts ...RunDaprdOption) *Command {
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

	defaultExitCode := 0
	if runtime.GOOS == "windows" {
		// Windows returns 1 when we kill the process.
		defaultExitCode = 1
	}

	fp := freeport.New(t, 6)
	options := daprdOptions{
		stdout:           iowriter.New(t),
		stderr:           iowriter.New(t),
		binPath:          os.Getenv("DAPR_INTEGRATION_DAPRD_PATH"),
		appID:            uid.String(),
		appPort:          appListener.Addr().(*net.TCPAddr).Port,
		grpcPort:         fp.Port(t, 0),
		httpPort:         fp.Port(t, 1),
		internalGRPCPort: fp.Port(t, 2),
		publicPort:       fp.Port(t, 3),
		metricsPort:      fp.Port(t, 4),
		profilePort:      fp.Port(t, 5),
		runErrorFn: func(err error) {
			if runtime.GOOS == "windows" {
				// Windows returns 1 when we kill the process.
				assert.ErrorContains(t, err, "exit status 1")
			} else {
				assert.NoError(t, err, "expected daprd to run without error")
			}
		},
		exitCode: defaultExitCode,
	}

	for _, opt := range opts {
		opt(&options)
	}

	args := []string{
		"--log-level=" + "debug",
		"--app-id=" + options.appID,
		"--app-port=" + strconv.Itoa(options.appPort),
		"--dapr-grpc-port=" + strconv.Itoa(options.grpcPort),
		"--dapr-http-port=" + strconv.Itoa(options.httpPort),
		"--dapr-internal-grpc-port=" + strconv.Itoa(options.internalGRPCPort),
		"--dapr-public-port=" + strconv.Itoa(options.publicPort),
		"--metrics-port=" + strconv.Itoa(options.metricsPort),
		"--profile-port=" + strconv.Itoa(options.profilePort),
		"--enable-app-health-check=" + strconv.FormatBool(options.appHealthCheck),
		"--app-health-probe-interval=" + strconv.Itoa(options.appHealthProbeInterval),
		"--app-health-threshold=" + strconv.Itoa(options.appHealthProbeThreshold),
	}
	if options.appHealthCheckPath != "" {
		args = append(args, "--app-health-check-path="+options.appHealthCheckPath)
	}

	t.Logf("Running daprd with args: %s %s", options.binPath, strings.Join(args, " "))
	ctx, cancel := context.WithCancel(ctx)
	//nolint:gosec
	cmd := exec.CommandContext(ctx, options.binPath, args...)

	cmd.Stdout = options.stdout
	cmd.Stderr = options.stderr

	daprd := &Command{
		cmdcancel:        cancel,
		cmd:              cmd,
		stdoutpipe:       options.stdout,
		stderrpipe:       options.stderr,
		AppID:            options.appID,
		AppPort:          options.appPort,
		GRPCPort:         options.grpcPort,
		HTTPPort:         options.httpPort,
		InternalGRPCPort: options.internalGRPCPort,
		PublicPort:       options.publicPort,
		MetricsPort:      options.metricsPort,
		ProfilePort:      options.profilePort,
		runErrorFnFn:     options.runErrorFn,
		exitCode:         options.exitCode,
	}

	fp.Free(t)
	require.NoError(t, cmd.Start())

	return daprd
}

func (c *Command) Cleanup(t *testing.T) {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()

	assert.NoError(t, c.stderrpipe.Close())
	assert.NoError(t, c.stdoutpipe.Close())

	kill.Kill(t, c.cmd)
	c.checkExit(t)
}

func (c *Command) PID(t *testing.T) int {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()
	assert.NotNil(t, c.cmd.Process, "PID called but process is nil")
	return c.cmd.Process.Pid
}

func (c *Command) checkExit(t *testing.T) {
	t.Helper()

	t.Log("waiting for daprd process to exit")

	c.runErrorFnFn(c.cmd.Wait())
	assert.NotNil(t, c.cmd.ProcessState, "process state should not be nil")
	assert.Equalf(t, c.exitCode, c.cmd.ProcessState.ExitCode(), "expected exit code to be %d", c.exitCode)
}
