/*
Copyright 2025 The Dapr Authors
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

package routes

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(routes))
}

const daprAppIdHeader = "Dapr-App-Id"

type routes struct {
	daprd                      *daprd.Daprd
	app                        *app.App
	daprSubscribeHeaderPresent atomic.Pointer[string]
	daprConfigHeaderPresent    atomic.Pointer[string]
}

func (r *routes) Setup(t *testing.T) []framework.Option {
	appOpts := make([]app.Option, 0)

	appOpts = append(appOpts,
		app.WithHandlerFunc("/dapr/subscribe", func(w http.ResponseWriter, req *http.Request) {
			r.daprSubscribeHeaderPresent.Store(ptr.Of(req.Header.Get(daprAppIdHeader)))
		}),
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			r.daprConfigHeaderPresent.Store(ptr.Of(req.Header.Get(daprAppIdHeader)))
		}))

	r.app = app.New(t, appOpts...)
	r.daprd = daprd.New(t,
		daprd.WithAppPort(r.app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`),
	)

	return []framework.Option{
		framework.WithProcesses(r.daprd, r.app),
	}
}

func (r *routes) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	daprSubscribeHeader := r.daprSubscribeHeaderPresent.Load()
	require.NotNil(t, daprSubscribeHeader)
	assert.Equal(t, r.daprd.AppID(), *daprSubscribeHeader)

	daprConfigHeader := r.daprConfigHeaderPresent.Load()
	require.NotNil(t, daprConfigHeader)
	assert.Equal(t, r.daprd.AppID(), *daprConfigHeader)
}
