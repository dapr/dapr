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
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func init() {
	suite.Register(new(routes))
}

type routes struct {
	daprd                      *procdaprd.Daprd
	app                        *app.App
	daprSubscribeHeaderPresent atomic.Bool
	daprConfigHeaderPresent    atomic.Bool
	daprAppID                  string
}

func (r *routes) Setup(t *testing.T) []framework.Option {
	appOpts := make([]app.Option, 0)

	appOpts = append(appOpts,
		app.WithHandlerFunc("/dapr/subscribe", func(w http.ResponseWriter, req *http.Request) {
			get := req.Header.Get("Dapr-App-Id")
			if get == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if get == r.daprAppID {
				r.daprSubscribeHeaderPresent.CompareAndSwap(false, true)
			}
		}),
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			get := req.Header.Get("Dapr-App-Id")
			if get == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if get == r.daprAppID {
				r.daprConfigHeaderPresent.CompareAndSwap(false, true)
			}
		}))

	r.daprAppID = uuid.NewString()
	r.app = app.New(t, appOpts...)
	r.daprd = procdaprd.New(t,
		procdaprd.WithAppID(r.daprAppID),
		procdaprd.WithAppPort(r.app.Port()),
		procdaprd.WithAppProtocol("http"),
		procdaprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
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
	r.app.Run(t, ctx)
	t.Cleanup(func() { r.app.Cleanup(t) })

	assert.True(t, r.daprSubscribeHeaderPresent.Load(), "dapr subscribe header not present")
	assert.True(t, r.daprConfigHeaderPresent.Load(), "dapr config header not present")
}
