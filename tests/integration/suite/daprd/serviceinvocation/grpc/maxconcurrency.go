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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	suite.Register(new(maxconcurrency))
}

type maxconcurrency struct {
	daprd *procdaprd.Daprd
	app   *app.App

	inflight    atomic.Int64
	maxInflight atomic.Int64
	entered     chan struct{}
	release     chan struct{}
}

func (m *maxconcurrency) Setup(t *testing.T) []framework.Option {
	m.entered = make(chan struct{}, 1)
	m.release = make(chan struct{})

	m.app = app.New(t, app.WithOnInvokeFn(func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		switch in.GetMethod() {
		case "hold":
			select {
			case m.entered <- struct{}{}:
			default:
			}
			select {
			case <-m.release:
			case <-ctx.Done():
			}
		case "counted":
			cur := m.inflight.Add(1)
			for {
				max := m.maxInflight.Load()
				if cur <= max || m.maxInflight.CompareAndSwap(max, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond * 300)
			m.inflight.Add(-1)
		case "fast":
		}
		return new(commonv1.InvokeResponse), nil
	}))

	m.daprd = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(m.app.Port(t)),
		procdaprd.WithAppMaxConcurrency(1),
	)

	return []framework.Option{
		framework.WithProcesses(m.app, m.daprd),
	}
}

func (m *maxconcurrency) Run(t *testing.T, ctx context.Context) {
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

	errCh := make(chan error, 1)
	go func() { errCh <- invoke(ctx, "fast") }()
	select {
	case err := <-errCh:
		require.NoError(t, err, "single invocation failed")
	case <-time.After(time.Second * 10):
		t.Fatal("single invocation did not complete: limiter slot double-released, response held until the next request arrives")
	}
	require.NoError(t, invoke(ctx, "fast"), "second invocation failed")

	done := make(chan error, 3)
	for range 3 {
		go func() { done <- invoke(ctx, "counted") }()
	}
	for range 3 {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			t.Fatal("concurrent invocations did not complete")
		}
	}
	assert.Equal(t, int64(1), m.maxInflight.Load(), "app observed overlapping invocations despite app-max-concurrency=1")

	// A waiter whose context dies while queued on the full limiter must
	// return promptly and must not consume or leak the slot.
	holdErr := make(chan error, 1)
	go func() { holdErr <- invoke(ctx, "hold") }()
	select {
	case <-m.entered:
	case <-time.After(time.Second * 10):
		t.Fatal("hold invocation never reached the app")
	}

	shortCtx, shortCancel := context.WithTimeout(ctx, time.Second*2)
	t.Cleanup(shortCancel)
	waiterErr := make(chan error, 1)
	go func() { waiterErr <- invoke(shortCtx, "fast") }()
	select {
	case err := <-waiterErr:
		require.Error(t, err, "queued waiter returned without error despite cancelled context")
		require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	case <-time.After(time.Second * 5):
		t.Fatal("queued waiter did not return after its context was cancelled")
	}

	close(m.release)
	select {
	case err := <-holdErr:
		require.NoError(t, err, "held invocation failed after release")
	case <-time.After(time.Second * 10):
		t.Fatal("held invocation did not complete after release")
	}

	fastErr := make(chan error, 1)
	go func() { fastErr <- invoke(ctx, "fast") }()
	select {
	case err := <-fastErr:
		require.NoError(t, err, "invocation after cancelled waiter failed: limiter slot leaked")
	case <-time.After(time.Second * 10):
		t.Fatal("invocation after cancelled waiter did not complete: limiter slot leaked")
	}
}
