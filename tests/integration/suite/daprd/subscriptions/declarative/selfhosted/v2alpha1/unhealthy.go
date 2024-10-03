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
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unhealthy))
}

type unhealthy struct {
	daprd     *daprd.Daprd
	app       *app.App
	appCalled atomic.Bool
}

func (u *unhealthy) Setup(t *testing.T) []framework.Option {
	u.app = app.New(t,
		app.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
			u.appCalled.Store(true)
		}),
		app.WithInitialHealth(false),
	)

	u.daprd = daprd.New(t,
		daprd.WithAppPort(u.app.Port()),
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
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
`))

	return []framework.Option{
		framework.WithProcesses(u.app, u.daprd),
	}
}

func (u *unhealthy) Run(t *testing.T, ctx context.Context) {
	u.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, u.daprd.GetMetaSubscriptions(t, ctx), 1)

	_, err := u.daprd.GRPCClient(t, ctx).PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)
	assert.False(t, u.appCalled.Load())
}
