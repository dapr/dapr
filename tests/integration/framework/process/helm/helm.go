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
	"context"
	"path/filepath"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// Helm test helm chart template.  It is not really an integration test but this seems the best place to place it
type Helm struct {
	exec process.Interface
}

func New(t *testing.T, fopts ...OptionFunc) *Helm {
	t.Helper()

	var opts options

	for _, fopt := range fopts {
		fopt(&opts)
	}

	// helm options (not all are available)
	args := make([]string, 0, 2*(len(opts.setValues)+len(opts.setStringValues)+len(opts.showOnly)))
	for _, v := range opts.setValues {
		args = append(args, "--set", v)
	}
	for _, v := range opts.setStringValues {
		args = append(args, "--set-string", v)
	}
	if opts.setJSONValue != nil {
		args = append(args, "--set-json", *opts.setJSONValue)
	}
	for _, v := range opts.showOnly {
		args = append(args, "--show-only", v)
	}
	if opts.namespace != nil {
		args = append(args, "--namespace", *opts.namespace)
	}

	args = append(args, filepath.Join(binary.GetRootDir(t), "charts", "dapr"))

	execOpts := opts.execOpts
	if opts.stdout != nil {
		execOpts = append(execOpts, exec.WithStdout(opts.stdout))
	}

	return &Helm{
		exec: exec.New(t, binary.EnvValue("helmtemplate"), args, execOpts...),
	}
}

func (h *Helm) Run(t *testing.T, ctx context.Context) {
	h.exec.Run(t, ctx)
}

func (h *Helm) Cleanup(t *testing.T) {
	h.exec.Cleanup(t)
}
