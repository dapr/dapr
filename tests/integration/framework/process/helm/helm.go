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
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

type LocalBuf struct {
	*bytes.Buffer
}

func (b LocalBuf) Close() error {
	return nil
}

// Helm test helm chart template.  It is not really an integration test but this seems the best place to place it
type Helm struct {
	exec   process.Interface
	stdout *LocalBuf
}

func New(t *testing.T, fopts ...OptionFunc) *Helm {
	t.Helper()

	var opts options

	for _, fopt := range fopts {
		fopt(&opts)
	}
	var args []string
	for _, v := range opts.setValues {
		args = append(args, "--set", v)
	}
	for _, v := range opts.setStringValues {
		args = append(args, "--set-string", v)
	}
	if opts.setJSONValue != "" {
		args = append(args, "--set-json", opts.setJSONValue)
	}
	for _, v := range opts.showOnly {
		args = append(args, "--show-only", v)
	}
	var localBuf *LocalBuf
	if opts.useLocalBuffForStdout {
		localBuf = &LocalBuf{Buffer: &bytes.Buffer{}}
		opts.execOpts = append(opts.execOpts, exec.WithStdout(localBuf))
	}
	opts.execOpts = append(opts.execOpts, exec.WaitForCompletion())
	rootDir := binary.GetRootDir(t)
	chartsPath := filepath.Join(rootDir, "charts", "dapr")
	args = append(args, chartsPath)

	return &Helm{
		exec:   exec.New(t, binary.EnvValue("helmtemplate"), args, opts.execOpts...),
		stdout: localBuf,
	}
}

func (h *Helm) Run(t *testing.T, ctx context.Context) {
	h.exec.Run(t, ctx)
}

func (h *Helm) Cleanup(t *testing.T) {
	h.exec.Cleanup(t)
}

func (h *Helm) GetStdout() []byte {
	return h.stdout.Bytes()
}
