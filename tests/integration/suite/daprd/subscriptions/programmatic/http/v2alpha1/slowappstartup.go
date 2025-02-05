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

package v2alpha1

import (
	"context"
	nethttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(slowappstartup))
}

type slowappstartup struct {
	daprd          *daprd.Daprd
	healthCalled   atomic.Int64
	isHealthy      atomic.Bool
	listSubsCalled atomic.Int64
}

func (s *slowappstartup) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/dapr/config", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			s.listSubsCalled.Add(1)
			w.Write([]byte(`{}`))
		}),
		app.WithHandlerFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			s.healthCalled.Add(1)
			if s.isHealthy.Load() {
				w.WriteHeader(nethttp.StatusOK)
			} else {
				w.WriteHeader(nethttp.StatusServiceUnavailable)
			}
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, s.daprd),
	}
}

func (s *slowappstartup) Run(t *testing.T, ctx context.Context) {
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, s.healthCalled.Load(), int64(1))
	}, time.Second*15, time.Millisecond*10)
	assert.Equal(t, int64(0), s.listSubsCalled.Load())

	s.isHealthy.Store(true)

	s.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), s.listSubsCalled.Load())
	}, time.Second*5, time.Millisecond*10)
}
