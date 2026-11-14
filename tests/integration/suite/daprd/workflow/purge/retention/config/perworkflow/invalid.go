/*
Copyright 2026 The Dapr Authors
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
package perworkflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invalid))
}

// invalid: daprd refuses to start on a malformed workflows list.
type invalid struct {
	loglines []*logline.LogLine
}

func (i *invalid) Setup(t *testing.T) []framework.Option {
	cases := []struct{ spec, log string }{
		{
			spec: `
        - completed: "1s"`,
			log: "workflow stateRetentionPolicy.workflows entries require a name",
		},
		{
			spec: `
        - name: order
          completed: "1s"
        - name: order
          failed: "1s"`,
			log: "duplicate workflow name",
		},
		{
			spec: `
        - name: order`,
			log: "in workflow stateRetentionPolicy.workflows sets no retention duration",
		},
	}

	procs := make([]process.Interface, 0, len(cases)*2)
	for _, tc := range cases {
		ll := logline.New(t, logline.WithStdoutLineContains(tc.log))
		d := daprd.New(t,
			policy(t, `      workflows:`+tc.spec+"\n"),
			daprd.WithExecOptions(
				exec.WithExitCode(1),
				exec.WithRunError(func(t *testing.T, err error) {
					require.ErrorContains(t, err, "exit status 1")
				}),
				exec.WithStdout(ll.Stdout()),
			),
		)
		i.loglines = append(i.loglines, ll)
		procs = append(procs, ll, d)
	}

	return []framework.Option{framework.WithProcesses(procs...)}
}

func (i *invalid) Run(t *testing.T, ctx context.Context) {
	for _, ll := range i.loglines {
		ll.EventuallyFoundAll(t)
	}
}
