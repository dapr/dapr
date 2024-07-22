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
	oexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/process/exec/kill"
	"github.com/dapr/dapr/tests/integration/framework/tee"
)

type Option func(*options)

type exec struct {
	cmd *oexec.Cmd

	args       []string
	binPath    string
	runErrorFn func(*testing.T, error)
	exitCode   int
	envs       map[string]string
	stdoutpipe io.WriteCloser
	stderrpipe io.WriteCloser

	wg sync.WaitGroup
}

func New(t *testing.T, binPath string, args []string, fopts ...Option) *exec {
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
				require.ErrorContains(t, err, "exit status 1")
			} else {
				require.NoError(t, err, "expected %q to run without error", binPath)
			}
		},
		exitCode: defaultExitCode,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &exec{
		binPath:    binPath,
		args:       args,
		envs:       opts.envs,
		stdoutpipe: opts.stdout,
		stderrpipe: opts.stderr,
		runErrorFn: opts.runErrorFn,
		exitCode:   opts.exitCode,
	}
}

func (e *exec) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Logf("Running %q with args: %s %s", filepath.Base(e.binPath), e.binPath, strings.Join(e.args, " "))

	//nolint:gosec
	e.cmd = oexec.CommandContext(ctx, e.binPath, e.args...)

	stdoutPipe, err := e.cmd.StdoutPipe()
	require.NoError(t, err)
	stderrPipe, err := e.cmd.StderrPipe()
	require.NoError(t, err)

	stdoutpipe := tee.NewWriterCloser(
		iowriter.New(t, filepath.Base(e.binPath)),
		e.stdoutpipe,
	)
	stderrpipe := tee.NewWriterCloser(
		iowriter.New(t, filepath.Base(e.binPath)),
		e.stderrpipe,
	)

	e.wg.Add(2)
	go func() {
		defer e.wg.Done()
		io.Copy(stdoutpipe, stdoutPipe)
		require.NoError(t, stdoutpipe.Close())
	}()
	go func() {
		defer e.wg.Done()
		io.Copy(stderrpipe, stderrPipe)
		require.NoError(t, stderrpipe.Close())
	}()

	// Wait for a few seconds before killing the process completely.
	e.cmd.WaitDelay = time.Second * 5

	for k, v := range e.envs {
		e.cmd.Env = append(e.cmd.Env, k+"="+v)
	}

	require.NoError(t, e.cmd.Start())
}

func (e *exec) Cleanup(t *testing.T) {
	defer e.wg.Wait()

	kill.Kill(t, e.cmd)
	e.checkExit(t)
}

func (e *exec) checkExit(t *testing.T) {
	t.Helper()

	t.Logf("waiting for %q process to exit", filepath.Base(e.binPath))

	e.runErrorFn(t, e.cmd.Wait())
	require.NotNil(t, e.cmd.ProcessState, "process state should not be nil")
	assert.Equalf(t, e.exitCode, e.cmd.ProcessState.ExitCode(), "expected exit code to be %d", e.exitCode)
	t.Logf("%q process exited", filepath.Base(e.binPath))
}
