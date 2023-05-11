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

package base

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/base/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/process/base/kill"
)

type options struct {
	stdout io.WriteCloser
	stderr io.WriteCloser

	runErrorFn func(error)
	exitCode   int
}

type Option func(*options)

type base struct {
	lock sync.Mutex
	cmd  *exec.Cmd

	args       []string
	binPath    string
	runErrorFn func(error)
	exitCode   int
	stdoutpipe io.WriteCloser
	stderrpipe io.WriteCloser
}

func New(t *testing.T, binPath string, args []string, fopts ...Option) *base {
	t.Helper()

	defaultExitCode := 0
	if runtime.GOOS == "windows" {
		// Windows returns 1 when we kill the process.
		defaultExitCode = 1
	}

	opts := options{
		stdout: iowriter.New(t, filepath.Base(binPath)),
		stderr: iowriter.New(t, filepath.Base(binPath)),
		runErrorFn: func(err error) {
			t.Helper()
			if runtime.GOOS == "windows" {
				// Windows returns 1 when we kill the process.
				assert.ErrorContains(t, err, "exit status 1")
			} else {
				assert.NoError(t, err, "expected %q to run without error", binPath)
			}
		},
		exitCode: defaultExitCode,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &base{
		binPath:    binPath,
		args:       args,
		stdoutpipe: opts.stdout,
		stderrpipe: opts.stderr,
		runErrorFn: opts.runErrorFn,
		exitCode:   opts.exitCode,
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	b.lock.Lock()
	defer b.lock.Unlock()

	t.Logf("Running %q with args: %s %s", filepath.Base(b.binPath), b.binPath, strings.Join(b.args, " "))

	//nolint:gosec
	b.cmd = exec.CommandContext(ctx, b.binPath, b.args...)

	b.cmd.Stdout = b.stdoutpipe
	b.cmd.Stderr = b.stderrpipe

	require.NoError(t, b.cmd.Start())
}

func (b *base) Cleanup(t *testing.T) {
	t.Helper()
	b.lock.Lock()
	defer b.lock.Unlock()

	assert.NoError(t, b.stderrpipe.Close())
	assert.NoError(t, b.stdoutpipe.Close())

	kill.Kill(t, b.cmd)
	b.checkExit(t)
}

func (b *base) checkExit(t *testing.T) {
	t.Helper()

	t.Logf("waiting for %q process to exit", filepath.Base(b.binPath))

	b.runErrorFn(b.cmd.Wait())
	assert.NotNil(t, b.cmd.ProcessState, "process state should not be nil")
	assert.Equalf(t, b.exitCode, b.cmd.ProcessState.ExitCode(), "expected exit code to be %d", b.exitCode)
}
