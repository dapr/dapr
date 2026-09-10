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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	fakeplacement "github.com/dapr/dapr/tests/integration/framework/process/grpc/placement"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unimplemented))
}

// unimplemented asserts daprd falls back to its own dissemination timeout,
// and still completes dissemination rounds, against a placement server that
// predates the Config RPC.
type unimplemented struct {
	daprd *daprd.Daprd
	place *fakeplacement.Placement
	ll    *logline.LogLine
}

func (u *unimplemented) Setup(t *testing.T) []framework.Option {
	u.place = fakeplacement.New(t)

	u.ll = logline.New(t,
		logline.WithStdoutLineContains(
			"Placement service does not implement the Config RPC; using the local dissemination timeout for drain clamping",
		),
	)

	app := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactor"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	u.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(u.place.Address(t)),
		daprd.WithAppPort(app.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithLogLineStdout(u.ll),
	)

	return []framework.Option{
		framework.WithProcesses(u.place, app, u.ll, u.daprd),
	}
}

func (u *unimplemented) Run(t *testing.T, ctx context.Context) {
	u.daprd.WaitUntilRunning(t, ctx)

	u.ll.EventuallyFoundAll(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, u.place.ConfigCalled(), int64(1))
		assert.GreaterOrEqual(c, u.place.RoundsCompleted(), int64(1))
	}, time.Second*10, time.Millisecond*10)
}
