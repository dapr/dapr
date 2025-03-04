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

package actors

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
	daprdNoPlace                         *daprd.Daprd
	daprdPlaceEmptyAddr                  *daprd.Daprd
	daprdPlaceSpaceAddr                  *daprd.Daprd
	daprdPlaceExtraSpaceAddr             *daprd.Daprd
	daprdPlaceSingleQuoteAddr            *daprd.Daprd
	daprdPlaceSingleQuoteExtraSpaceAddr  *daprd.Daprd
	daprdPlaceSingleQuoteEmtpyStringAddr *daprd.Daprd
	daprdPlaceDoubleQuoteAddr            *daprd.Daprd
	daprdPlaceDoubleQuoteSpaceAddr       *daprd.Daprd

	loglineActorsDisabled *logline.LogLine
}

func (a *emptyaddress) Setup(t *testing.T) []framework.Option {
	a.loglineActorsDisabled = logline.New(t, logline.WithStdoutLineContains(
		"Actor runtime disabled",
	))

	a.daprdNoPlace = daprd.New(t,
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceEmptyAddr = daprd.New(t,
		daprd.WithPlacementAddresses(""),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceSpaceAddr = daprd.New(t,
		daprd.WithPlacementAddresses(" "),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceExtraSpaceAddr = daprd.New(t,
		daprd.WithPlacementAddresses("       "),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceSingleQuoteAddr = daprd.New(t,
		daprd.WithPlacementAddresses("''"),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceSingleQuoteExtraSpaceAddr = daprd.New(t,
		daprd.WithPlacementAddresses("'      '"),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceSingleQuoteEmtpyStringAddr = daprd.New(t,
		daprd.WithPlacementAddresses(`'""'`),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceDoubleQuoteAddr = daprd.New(t,
		daprd.WithPlacementAddresses(`""`),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	a.daprdPlaceDoubleQuoteSpaceAddr = daprd.New(t,
		daprd.WithPlacementAddresses(`"   "`),
		daprd.WithExecOptions(exec.WithStdout(a.loglineActorsDisabled.Stdout())),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(a.loglineActorsDisabled, a.daprdNoPlace, a.daprdPlaceEmptyAddr,
			a.daprdPlaceSpaceAddr, a.daprdPlaceExtraSpaceAddr, a.daprdPlaceSingleQuoteAddr,
			a.daprdPlaceSingleQuoteExtraSpaceAddr, a.daprdPlaceSingleQuoteEmtpyStringAddr,
			a.daprdPlaceDoubleQuoteAddr, a.daprdPlaceDoubleQuoteSpaceAddr),
	}
}

func (a *emptyaddress) Run(t *testing.T, ctx context.Context) {
	a.daprdNoPlace.WaitUntilRunning(t, ctx)
	a.daprdPlaceSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceExtraSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceEmptyAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceSingleQuoteAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceSingleQuoteExtraSpaceAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceSingleQuoteEmtpyStringAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceDoubleQuoteAddr.WaitUntilRunning(t, ctx)
	a.daprdPlaceDoubleQuoteSpaceAddr.WaitUntilRunning(t, ctx)

	a.loglineActorsDisabled.EventuallyFoundAll(t)
}
