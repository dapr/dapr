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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	runError                func(error)
	exitCode                int
	appHealthCheck          bool
	appHealthCheckPath      string
	appHealthProbeInterval  int
	appHealthProbeThreshold int
}

// RunDaprdOption is a function that configures the DaprdOptions.
type RunDaprdOption func(*daprdOptions)

type Command struct {
	lock        sync.Mutex
	cmd         *exec.Cmd
	appListener net.Listener

	runError   func(error)
	exitCode   int
	stdoutpipe io.WriteCloser
	stderrpipe io.WriteCloser

	AppID            string
	AppPort          int
	GrpcPort         int
	HttpPort         int
	InternalGRPCPort int
	PublicPort       int
	MetricsPort      int
	ProfilePort      int
}

func RunDaprd(t *testing.T, ctx context.Context, opts ...RunDaprdOption) *Command {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	appListener, err := net.Listen("tcp", ":0")
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

	options := daprdOptions{
		stdout:           newStdWriter(),
		stderr:           newStdWriter(),
		binPath:          os.Getenv("DAPR_INTEGRATION_DAPRD_PATH"),
		appID:            uid.String(),
		appPort:          appListener.Addr().(*net.TCPAddr).Port,
		grpcPort:         freeport(t),
		httpPort:         freeport(t),
		internalGRPCPort: freeport(t),
		publicPort:       freeport(t),
		metricsPort:      freeport(t),
		profilePort:      freeport(t),
		runError: func(err error) {
			assert.NoError(t, err, "expected daprd to run without error")
		},
		exitCode: 0,
	}

	for _, opt := range opts {
		opt(&options)
	}

	args := []string{
		"--log-level", "debug",
		"--app-id", options.appID,
		"--app-port", strconv.Itoa(options.appPort),
		"--dapr-grpc-port", strconv.Itoa(options.grpcPort),
		"--dapr-http-port", strconv.Itoa(options.httpPort),
		"--dapr-internal-grpc-port", strconv.Itoa(options.internalGRPCPort),
		"--dapr-public-port", strconv.Itoa(options.publicPort),
		"--metrics-port", strconv.Itoa(options.metricsPort),
		"--profile-port", strconv.Itoa(options.profilePort),
		"--enable-app-health-check", strconv.FormatBool(options.appHealthCheck),
		"--app-health-check-path", options.appHealthCheckPath,
		"--app-health-check-interval", strconv.Itoa(options.appHealthProbeInterval),
		"--app-health-threshold", strconv.Itoa(options.appHealthProbeThreshold),
	}
	t.Logf("Running daprd with args: %s %s", options.binPath, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, options.binPath, args...)

	cmd.Stdout = options.stdout
	cmd.Stderr = options.stderr

	daprd := &Command{
		cmd:              cmd,
		stdoutpipe:       options.stdout,
		stderrpipe:       options.stderr,
		AppID:            options.appID,
		AppPort:          options.appPort,
		GrpcPort:         options.grpcPort,
		HttpPort:         options.httpPort,
		InternalGRPCPort: options.internalGRPCPort,
		PublicPort:       options.publicPort,
		MetricsPort:      options.metricsPort,
		ProfilePort:      options.profilePort,
		runError:         options.runError,
		exitCode:         options.exitCode,
	}

	require.NoError(t, cmd.Start())

	return daprd
}

func (c *Command) Cleanup(t *testing.T) {
	t.Helper()
	c.Kill(t)
	c.checkExit(t)
}

func (c *Command) PID(t *testing.T) int {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()
	assert.NotNil(t, c.cmd.Process, "PID called but process is nil")
	return c.cmd.Process.Pid
}

func (c *Command) Kill(t *testing.T) {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()

	t.Log("killing daprd process")

	if c.cmd == nil || c.cmd.ProcessState != nil {
		return
	}

	assert.NoError(t, c.stderrpipe.Close())
	assert.NoError(t, c.stdoutpipe.Close())
	assert.NoError(t, c.cmd.Process.Signal(os.Interrupt))

	// TODO: daprd does not currently gracefully exit on a single interrupt
	// signal. Remove once fixed.
	time.Sleep(time.Millisecond * 300)
	assert.NoError(t, c.cmd.Process.Signal(os.Interrupt))
}

func (c *Command) checkExit(t *testing.T) {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()

	t.Log("waiting for daprd process to exit")

	c.runError(c.cmd.Wait())
	assert.NotNil(t, c.cmd.ProcessState, "process state should not be nil")
	assert.Equalf(t, c.exitCode, c.cmd.ProcessState.ExitCode(), "expected exit code to be %d", c.exitCode)
}

func freeport(t *testing.T) int {
	n, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer n.Close()
	return n.Addr().(*net.TCPAddr).Port
}
