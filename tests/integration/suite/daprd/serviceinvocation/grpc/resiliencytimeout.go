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

package grpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resiliencytimeout))
}

type resiliencytimeout struct {
	daprd *procdaprd.Daprd

	entered chan struct{}
	release chan struct{}
}

func (m *resiliencytimeout) Setup(t *testing.T) []framework.Option {
	m.entered = make(chan struct{}, 1)
	m.release = make(chan struct{})

	appID := uuid.New().String()

	srv := app.New(t, app.WithOnInvokeFn(func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		if in.GetMethod() == "hold" {
			select {
			case m.entered <- struct{}{}:
			default:
			}
			select {
			case <-m.release:
			case <-ctx.Done():
			}
		}
		return new(commonv1.InvokeResponse), nil
	}))

	m.daprd = procdaprd.New(t,
		procdaprd.WithAppID(appID),
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv.Port(t)),
		procdaprd.WithAppMaxConcurrency(1),
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      short: 1s
  targets:
    apps:
      %s:
        timeout: short
`, appID)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, m.daprd),
	}
}

func (m *resiliencytimeout) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)
	invoke := func(ctx context.Context, method string) error {
		_, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: m.daprd.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        method,
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
			},
		})
		return err
	}

	holdErr := make(chan error, 1)
	go func() { holdErr <- invoke(ctx, "hold") }()
	select {
	case <-m.entered:
	case <-time.After(time.Second * 10):
		t.Fatal("hold invocation never reached the app")
	}

	waiterErr := make(chan error, 1)
	go func() { waiterErr <- invoke(ctx, "fast") }()
	select {
	case err := <-waiterErr:
		require.Error(t, err, "queued waiter must fail on the resiliency timeout")
		require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	case <-time.After(time.Second * 10):
		t.Fatal("queued waiter did not return after the resiliency timeout")
	}

	close(m.release)
	select {
	case err := <-holdErr:
		require.Error(t, err, "held invocation must fail on the resiliency timeout")
		require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	case <-time.After(time.Second * 10):
		t.Fatal("held invocation did not return")
	}

	require.NoError(t, invoke(ctx, "fast"), "invocation after release failed")
}
