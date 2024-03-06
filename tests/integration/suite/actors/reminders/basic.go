/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or impliei.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reminders

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

// basic tests the basic functionality of actor reminders.
type basic struct {
	daprd *daprd.Daprd
	place *placement.Placement

	methodcalled atomic.Int64
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/remindermethod", func(w http.ResponseWriter, r *http.Request) {
		b.methodcalled.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	b.place = placement.New(t)
	b.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(b.place, srv, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	daprdURL := "http://localhost:" + strconv.Itoa(b.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := client.Do(req)
		//nolint:testifylint
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*100, "actor not ready in time")

	body := `{"dueTime": "0ms"}`
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/reminders/remindermethod", strings.NewReader(body))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	assert.Eventually(t, func() bool {
		return b.methodcalled.Load() == 1
	}, time.Second*3, time.Millisecond*100)

	conn, err := grpc.DialContext(ctx, b.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	gclient := rtv1.NewDaprClient(conn)

	_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "remindermethod",
		DueTime:   "0ms",
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return b.methodcalled.Load() == 2
	}, time.Second*3, time.Millisecond*100)
}
