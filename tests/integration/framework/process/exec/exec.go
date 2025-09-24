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

package exec

import (
	"context"
	"io"
	"os"
	oexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/process/exec/kill"
	"github.com/dapr/dapr/tests/integration/framework/tee"
	"github.com/dapr/dapr/utils"
)

type Option func(*options)

type Exec struct {
	cmd *oexec.Cmd

	args       []string
	binPath    string
	runErrorFn func(*testing.T, error)
	exitCode   int
	envs       map[string]string
	stdoutpipe io.WriteCloser
	stderrpipe io.WriteCloser

	wg   sync.WaitGroup
	once atomic.Bool
}

func New(t *testing.T, binPath string, args []string, fopts ...Option) *Exec {
	t.Helper()

	defaultExitCode := 0
	if runtime.GOOS == "windows" {
		// Windows returns 1 when we kill the process.
		defaultExitCode = 1
	}

	opts := options{
		runErrorFn: func(t *testing.T, err error) {
			t.Helper()
			if runtime.GOOS == "windows" {
				// Windows returns 1 when we kill the process.
				assert.ErrorContains(t, err, "exit status 1")
			} else {
				assert.NoError(t, err, "expected %q to run without error", binPath)
			}
		},
		exitCode: defaultExitCode,
		envs: map[string]string{
			"DAPR_UNSAFE_SKIP_CONTAINER_UID_GID_CHECK": "true",
		},
	}

	if hostIPOverride := os.Getenv(utils.HostIPEnvVar); hostIPOverride != "" {
		opts.envs[utils.HostIPEnvVar] = hostIPOverride
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &Exec{
		binPath:    binPath,
		args:       args,
		envs:       opts.envs,
		stdoutpipe: opts.stdout,
		stderrpipe: opts.stderr,
		runErrorFn: opts.runErrorFn,
		exitCode:   opts.exitCode,
	}
}

func (e *Exec) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Logf("Running %q with args: %s %s", filepath.Base(e.binPath), e.binPath, strings.Join(e.args, " "))

	//nolint:gosec
	e.cmd = oexec.CommandContext(ctx, e.binPath, e.args...)

	iow := iowriter.New(t, filepath.Base(e.binPath))
	for _, pipe := range []struct {
		cmdPipeFn func() (io.ReadCloser, error)
		procPipe  io.WriteCloser
	}{
		{cmdPipeFn: e.cmd.StdoutPipe, procPipe: e.stdoutpipe},
		{cmdPipeFn: e.cmd.StderrPipe, procPipe: e.stderrpipe},
	} {
		cmdPipe, err := pipe.cmdPipeFn()
		require.NoError(t, err)

		pipe := tee.WriteCloser(iow, pipe.procPipe)

		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			io.Copy(pipe, cmdPipe)
			pipe.Close()
		}()
	}

	for k, v := range e.envs {
		e.cmd.Env = append(e.cmd.Env, k+"="+v)
	}

	require.NoError(t, e.cmd.Start())
}

func (e *Exec) Cleanup(t *testing.T) {
	defer func() { e.wg.Wait() }()

	if !e.once.CompareAndSwap(false, true) {
		return
	}

	kill.Interrupt(t, e.cmd)
	e.checkExit(t)
}

func (e *Exec) Kill(t *testing.T) {
	defer e.wg.Wait()

	if !e.once.CompareAndSwap(false, true) {
		return
	}

	kill.Kill(t, e.cmd)
}

func (e *Exec) checkExit(t *testing.T) {
	t.Helper()

	t.Logf("waiting for %q process to exit", filepath.Base(e.binPath))

	e.runErrorFn(t, e.cmd.Wait())
	assert.NotNil(t, e.cmd.ProcessState, "process state should not be nil")
	assert.Equalf(t, e.exitCode, e.cmd.ProcessState.ExitCode(), "expected exit code to be %d", e.exitCode)
	t.Logf("%q process exited", filepath.Base(e.binPath))
}
