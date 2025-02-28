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

package scheduler

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(emptyaddress))
}

type emptyaddress struct {
	daprdNoSched                        *daprd.Daprd
	daprdSchedEmptyAddr                 *daprd.Daprd
	daprdSchedSpaceAddr                 *daprd.Daprd
	daprdSchedExtraSpaceAddr            *daprd.Daprd
	daprdSchedSingleQuoteAddr           *daprd.Daprd
	daprdSchedSingleQuoteExtraSpaceAddr *daprd.Daprd
	daprdSchedDoubleQuoteAddr           *daprd.Daprd
	daprdSchedDoubleQuoteSpaceAddr      *daprd.Daprd

	loglineSchedDisabled *logline.LogLine
}

func (a *emptyaddress) Setup(t *testing.T) []framework.Option {
	a.loglineSchedDisabled = logline.New(t, logline.WithStdoutLineContains(
		"Scheduler disabled",
	))

	a.daprdNoSched = daprd.New(t,
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedEmptyAddr = daprd.New(t,
		daprd.WithSchedulerAddresses(""),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedSpaceAddr = daprd.New(t,
		daprd.WithSchedulerAddresses(" "),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedExtraSpaceAddr = daprd.New(t,
		daprd.WithSchedulerAddresses("       "),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedSingleQuoteAddr = daprd.New(t,
		daprd.WithSchedulerAddresses("''"),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedSingleQuoteExtraSpaceAddr = daprd.New(t,
		daprd.WithSchedulerAddresses("'      '"),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedDoubleQuoteAddr = daprd.New(t,
		daprd.WithSchedulerAddresses(`""`),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	a.daprdSchedDoubleQuoteSpaceAddr = daprd.New(t,
		daprd.WithSchedulerAddresses(`"   "`),
		daprd.WithExecOptions(exec.WithStdout(a.loglineSchedDisabled.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(a.loglineSchedDisabled, a.daprdNoSched,
			a.daprdSchedEmptyAddr, a.daprdSchedSpaceAddr, a.daprdSchedExtraSpaceAddr,
			a.daprdSchedSingleQuoteAddr, a.daprdSchedSingleQuoteExtraSpaceAddr,
			a.daprdSchedDoubleQuoteAddr, a.daprdSchedDoubleQuoteSpaceAddr),
	}
}

func (a *emptyaddress) Run(t *testing.T, ctx context.Context) {
	a.daprdNoSched.WaitUntilRunning(t, ctx)
	a.daprdSchedEmptyAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedExtraSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedSingleQuoteAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedSingleQuoteExtraSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedDoubleQuoteAddr.WaitUntilRunning(t, ctx)
	a.daprdSchedDoubleQuoteSpaceAddr.WaitUntilRunning(t, ctx)

	a.loglineSchedDisabled.EventuallyFoundAll(t)
}
