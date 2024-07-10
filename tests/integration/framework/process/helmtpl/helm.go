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

package helmtpl

import (
	"bytes"
	"context"
	"os"
	oexec "os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
)

// Helm test helm chart template.  It is not really an integration test but this seems the best place to place it
type Helm struct {
	defaultOpts options
}

func New(t *testing.T, fopts ...Option) *Helm {
	t.Helper()

	var opts options

	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &Helm{
		defaultOpts: opts,
	}
}

func (h *Helm) Render(t *testing.T, ctx context.Context, fopts ...Option) []byte {
	t.Helper()

	var opts options

	for _, fopt := range fopts {
		fopt(&opts)
	}

	if runtime.GOOS == "darwin" && os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("Skipping helm tests on macos in Github Actions. As Helm is not present in the macos runner.")
	}

	args := []string{"template"}
	opts = opts.merge(h.defaultOpts)
	args = append(args, opts.getHelmArgs()...)

	rootDir := binary.GetRootDir(t)
	chartsPath := filepath.Join(rootDir, "charts", "dapr")

	args = append(args, chartsPath)

	var stdout, stderr bytes.Buffer

	e := oexec.Command("helm", args...)
	e.Stdout = &stdout
	e.Stderr = &stderr
	err := e.Run()
	if opts.exitCode != nil {
		require.Equal(t, *opts.exitCode, e.ProcessState.ExitCode())
		if opts.exitErrorMsgRegexp != nil {
			require.Regexp(t, opts.exitErrorMsgRegexp, stderr.String())
		}
		return nil
	}
	require.NoError(t, err)
	return stdout.Bytes()
}

func (h *Helm) Run(t *testing.T, ctx context.Context) {}

func (h *Helm) Cleanup(t *testing.T) {}
