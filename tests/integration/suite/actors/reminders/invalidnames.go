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
	"strings"
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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invalidnames))
}

// invalidnames asserts that reminder names and actor ids containing characters
// that are unsafe in an etcd key or a callback URL are rejected. Names are
// rejected at the API edge; actor ids are rejected by the scheduler validator.
type invalidnames struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler
}

func (i *invalidnames) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	i.place = placement.New(t)
	i.sched = scheduler.New(t)
	i.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(i.sched),
	)

	return []framework.Option{
		framework.WithProcesses(i.sched, i.place, srv, i.daprd),
	}
}

func (i *invalidnames) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, i.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	gclient := rtv1.NewDaprClient(conn)

	cl := client.HTTP(t)
	actorURL := "http://localhost:" + strconv.Itoa(i.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, actorURL+"/method/foo", nil)
		require.NoError(c, rErr)
		resp, rErr := cl.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, 10*time.Second, 10*time.Millisecond)

	badNames := map[string]string{
		"empty":       "",
		"slash":       "bad/name",
		"backslash":   "bad\\name",
		"hash":        "bad#name",
		"question":    "bad?name",
		"nul":         "bad\x00name",
		"newline":     "bad\nname",
		"dot":         ".",
		"double dot":  "..",
		"over length": strings.Repeat("a", 600),
	}
	for name, reminder := range badNames {
		t.Run("name/"+name, func(t *testing.T) {
			_, rErr := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Name:      reminder,
				DueTime:   "0ms",
			})
			require.Error(t, rErr)
		})
	}

	badIDs := map[string]string{
		"slash":     "bad/id",
		"backslash": "bad\\id",
		"hash":      "bad#id",
		"question":  "bad?id",
		"nul":       "bad\x00id",
		"newline":   "bad\nid",
	}
	for name, id := range badIDs {
		t.Run("id/"+name, func(t *testing.T) {
			_, rErr := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "myactortype",
				ActorId:   id,
				Name:      "validname",
				DueTime:   "0ms",
			})
			require.Error(t, rErr)
		})
	}
}
