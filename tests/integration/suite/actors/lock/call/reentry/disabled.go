/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reentry

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(disabled))
}

type disabled struct {
	daprd  *daprd.Daprd
	called atomic.Int32
	rid    atomic.Pointer[string]
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithConfig(`{"entities":["reentrantActor"],"reentrancy":{"enabled":false}}`),
		app.WithHandlerFunc("/actors/reentrantActor/myactorid", func(_ http.ResponseWriter, r *http.Request) {}),
		app.WithHandlerFunc("/actors/reentrantActor/myactorid/method/foo", func(_ http.ResponseWriter, r *http.Request) {
			d.rid.Store(ptr.Of(r.Header.Get("Dapr-Reentrancy-Id")))
			d.called.Add(1)
		}),
	)

	place := placement.New(t)
	d.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, place, d.daprd),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/actors/reentrantActor/myactorid/method/foo", d.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, int32(1), d.called.Load())
	assert.Empty(t, *d.rid.Load())
}
