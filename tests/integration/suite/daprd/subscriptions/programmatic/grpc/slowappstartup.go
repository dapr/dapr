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

package grpc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
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
		app.WithListTopicSubscriptions(func(ctx context.Context, in *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			s.listSubsCalled.Add(1)
			return new(rtv1.ListTopicSubscriptionsResponse), nil
		}),
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			s.healthCalled.Add(1)
			if s.isHealthy.Load() {
				return new(rtv1.HealthCheckResponse), nil
			}
			return nil, errors.New("health error")
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(0), s.listSubsCalled.Load())

	s.isHealthy.Store(true)

	s.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), s.listSubsCalled.Load())
	}, time.Second*10, time.Millisecond*10)
}
