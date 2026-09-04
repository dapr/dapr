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

package config

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(advertise))
}

// advertise asserts daprd fetches the placement service configuration on
// connect and records the advertised dissemination timeout.
type advertise struct {
	actors *dactors.Actors
	ll     *logline.LogLine
}

func (a *advertise) Setup(t *testing.T) []framework.Option {
	a.ll = logline.New(t,
		logline.WithStdoutLineContains(
			"Placement advertised a dissemination timeout of 11s",
		),
	)

	place := placement.New(t,
		placement.WithDisseminateTimeout(time.Second*11),
	)

	a.actors = dactors.New(t,
		dactors.WithPlacement(place),
		dactors.WithActorTypes("myactor"),
		dactors.WithDaprdOptions(
			daprd.WithLogLineStdout(a.ll),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.ll, a.actors),
	}
}

func (a *advertise) Run(t *testing.T, ctx context.Context) {
	a.actors.WaitUntilRunning(t, ctx)
	a.ll.EventuallyFoundAll(t)
}
