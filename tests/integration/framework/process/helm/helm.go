/*
Copyright 2024 The Dapr Authors
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

package helm

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/tee"
)

// Helm test helm chart template.  It is not really an integration test but this seems the best place to place it
type Helm struct {
	exec   process.Interface
	stdout tee.Reader
	stderr tee.Reader
}

func New(t *testing.T, fopts ...OptionFunc) *Helm {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	// helm options (not all are available)
	args := make([]string, 0, 2*(len(opts.setValues)+len(opts.showOnly)))
	for _, v := range opts.setValues {
		args = append(args, "--set", v)
	}
	for _, v := range opts.showOnly {
		args = append(args, "--show-only", v)
	}
	if opts.namespace != nil {
		args = append(args, "--namespace", *opts.namespace)
	}

	args = append(args, filepath.Join(binary.RootDir(t), "charts", "dapr"))

	stdoutPipeR, stdoutPipeW := io.Pipe()
	stderrPipeR, stderrPipeW := io.Pipe()
	stdout := tee.Buffer(t, stdoutPipeR)
	stderr := tee.Buffer(t, stderrPipeR)

	execOpts := []exec.Option{
		// Since helmtemplate self exists, expect it to always run without error,
		// even on windows.
		exec.WithRunError(func(t *testing.T, err error) {
			t.Helper()
			require.NoError(t, err, "expected helmtemplate to run without error")
		}),
		exec.WithExitCode(0),
		exec.WithStdout(stdoutPipeW),
		exec.WithStderr(stderrPipeW),
	}

	return &Helm{
		exec:   exec.New(t, binary.EnvValue("helmtemplate"), args, execOpts...),
		stdout: stdout,
		stderr: stderr,
	}
}

func (h *Helm) Run(t *testing.T, ctx context.Context) {
	h.exec.Run(t, ctx)
}

func (h *Helm) Cleanup(t *testing.T) {
	_, err := io.ReadAll(h.Stdout(t))
	require.NoError(t, err)
	_, err = io.ReadAll(h.Stderr(t))
	require.NoError(t, err)
	h.exec.Cleanup(t)
}

func (h *Helm) Stdout(t *testing.T) io.Reader {
	return h.stdout.Add(t)
}

func (h *Helm) Stderr(t *testing.T) io.Reader {
	return h.stderr.Add(t)
}

func UnmarshalStdout[K any](t *testing.T, h *Helm) []K {
	t.Helper()

	s := bufio.NewScanner(h.Stdout(t))
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.Index(data, []byte("\n---")); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})

	var ks []K
	for s.Scan() {
		var k K
		require.NoError(t, yaml.Unmarshal(s.Bytes(), &k))
		ks = append(ks, k)
	}

	return ks
}
