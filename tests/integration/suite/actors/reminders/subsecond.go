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

package reminders

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(subsecond))
}

// subsecond guards the regression in which actor reminders fired ahead of
// their requested duration because RegisteredTime was serialized with
// whole-second precision and truncated to the prior whole second on read.
type subsecond struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler

	firedAt atomic.Int64
}

func (s *subsecond) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/subsecond", func(w http.ResponseWriter, r *http.Request) {
		// Record the first fire only; ignore late repeats.
		s.firedAt.CompareAndSwap(0, time.Now().UnixNano())
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	s.scheduler = procscheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(s.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.place, srv, s.daprd),
	}
}

func (s *subsecond) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	daprdURL := "http://localhost:" + strconv.Itoa(s.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"

	// Wait until the actor host is ready to receive invocations.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := httpClient.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	gclient := rtv1.NewDaprClient(conn)

	const requested = 2 * time.Second

	registeredAt := time.Now()
	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "subsecond",
		DueTime:   requested.String(),
	})
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotZero(c, s.firedAt.Load(), "reminder did not fire")
	}, requested+5*time.Second, 10*time.Millisecond)

	elapsed := time.Unix(0, s.firedAt.Load()).Sub(registeredAt)

	// The reminder must never fire before its requested due time. Prior
	// to sub-second precision support, this assertion failed by up to
	// ~1s because RegisteredTime was truncated to the previous whole
	// second on the daprd side before being sent to the scheduler.
	assert.GreaterOrEqual(t, elapsed, requested,
		"reminder fired early: elapsed=%s, requested=%s", elapsed, requested)
}
