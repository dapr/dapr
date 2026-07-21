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
	"sync"
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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(specialchars))
}

// specialchars verifies that actor reminders whose names and actor ids contain
// special characters (pipe '|', the '||' delimiter, and '@') can be registered
// through the scheduler service and fire. This covers the Schreder regression
// where actor ids embed '||' and reminder names contain '|'/'@'.
type specialchars struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	lock        sync.Mutex
	remindPaths []string
	fired       atomic.Int64
}

func (s *specialchars) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Catch-all so actor activation and reminder callbacks route regardless of
	// the special characters in the actor id / reminder name path segments.
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/method/remind/") {
			s.lock.Lock()
			s.remindPaths = append(s.remindPaths, r.URL.Path)
			s.lock.Unlock()
			s.fired.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	s.place = placement.New(t)
	s.sched = scheduler.New(t)
	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(s.sched),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.place, srv, s.daprd),
	}
}

func (s *specialchars) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	gclient := rtv1.NewDaprClient(conn)

	// Activate the actor so reminder callbacks have somewhere to land.
	cl := client.HTTP(t)
	actorURL := "http://localhost:" + strconv.Itoa(s.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, actorURL+"/method/foo", nil)
		require.NoError(c, rErr)
		resp, rErr := cl.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")

	t.Run("reminder name with pipe and at sign registers and fires", func(t *testing.T) {
		const reminderName = "remind|me@now"
		_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      reminderName,
			DueTime:   "0ms",
		})
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			s.lock.Lock()
			defer s.lock.Unlock()
			found := false
			for _, p := range s.remindPaths {
				if strings.HasSuffix(p, "/method/remind/"+reminderName) {
					found = true
					break
				}
			}
			assert.True(c, found, "reminder %q did not fire; paths seen: %v", reminderName, s.remindPaths)
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("reminder name with the || delimiter fires with its full name", func(t *testing.T) {
		// '||' is the scheduler's reserved internal key delimiter. A reminder
		// name that contains it must be delivered to the app in full, not
		// truncated on the last '||'.
		const reminderName = "remind||me||now"
		_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Name:      reminderName,
			DueTime:   "0ms",
		})
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			s.lock.Lock()
			defer s.lock.Unlock()
			found := false
			for _, p := range s.remindPaths {
				if strings.HasSuffix(p, "/method/remind/"+reminderName) {
					found = true
					break
				}
			}
			assert.True(c, found, "reminder %q did not fire with its full name; paths seen: %v", reminderName, s.remindPaths)
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("schreder-style actor id with || delimiter registers", func(t *testing.T) {
		// Actor id embeds the '||' delimiter and single pipes, exactly the
		// shape that previously failed RFC1123 validation in the scheduler.
		_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "nexus-fire-safety-api||SmokeDetectorGroupMonitorActor||Nexus|6671cfbe7f48af247700a24b||SmokeDetectorGroupInformation",
			Name:      "deviceupdate",
			DueTime:   "1000s",
		})
		require.NoError(t, err)
	})
}
