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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(refetch))
}

// refetch asserts daprd re-fetches the placement service configuration on
// every reconnect, picking up a changed dissemination timeout after the
// placement service restarts.
type refetch struct {
	daprd  *daprd.Daprd
	place1 *placement.Placement
	place2 *placement.Placement
	ll     *logline.LogLine
}

func (r *refetch) Setup(t *testing.T) []framework.Option {
	fp := ports.Reserve(t, 1)
	port := fp.Port(t)

	r.place1 = placement.New(t,
		placement.WithPort(port),
		placement.WithDisseminateTimeout(time.Second*9),
	)
	r.place2 = placement.New(t,
		placement.WithPort(port),
		placement.WithDisseminateTimeout(time.Second*12),
	)

	r.ll = logline.New(t,
		logline.WithStdoutLineContains(
			"Placement advertised a dissemination timeout of 9s",
			"Placement advertised a dissemination timeout of 12s",
		),
	)

	app := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`{"entities": ["myactor"]}`))
		}),
		prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place1.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithLogLineStdout(r.ll),
	)

	return []framework.Option{
		framework.WithProcesses(fp, app, r.ll, r.place1, r.daprd),
	}
}

func (r *refetch) Run(t *testing.T, ctx context.Context) {
	r.place1.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	// Restart placement on the same port with a different timeout; daprd
	// must fetch the new value on reconnect.
	r.place1.Cleanup(t)
	r.place2.Run(t, ctx)
	t.Cleanup(func() { r.place2.Cleanup(t) })
	r.place2.WaitUntilRunning(t, ctx)

	r.ll.EventuallyFoundAll(t)
}
