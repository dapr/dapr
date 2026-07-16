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

package reminders

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(hdata))
}

type hdata struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	lock sync.Mutex
	data map[string]chan string
}

func (h *hdata) Setup(t *testing.T) []framework.Option {
	h.data = make(map[string]chan string)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/{id}", func(w http.ResponseWriter, r *http.Request) {
	})
	handler.HandleFunc("/actors/myactortype/{id}/method/remind/", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		h.lock.Lock()
		ch := h.data[r.PathValue("id")]
		h.lock.Unlock()
		select {
		case ch <- string(b):
		case <-time.After(time.Second * 10):
		}
	})
	handler.HandleFunc("/actors/myactortype/{id}/method/foo", func(w http.ResponseWriter, r *http.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	h.place = placement.New(t)
	h.sched = scheduler.New(t)
	h.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(h.sched),
	)

	return []framework.Option{
		framework.WithProcesses(h.sched, h.place, srv, h.daprd),
	}
}

func (h *hdata) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	aurl := h.daprd.ActorInvokeURL("myactortype", "myactorid", "foo")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, aurl, nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := client.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	gclient := h.daprd.GRPCClient(t, ctx)

	tests := map[string]struct {
		expHTTP string
		expGRPC string
	}{
		``: {
			`{"dueTime":"","period":""}`,
			`{"dueTime":"","period":""}`,
		},
		`"foo"`: {
			`{"data":"foo","dueTime":"","period":""}`,
			`{"data":"ImZvbyI=","dueTime":"","period":""}`,
		},
		`{  "foo": [ 12, 4 ] }`: {
			`{"data":{"foo":[12,4]},"dueTime":"","period":""}`,
			`{"data":"eyAgImZvbyI6IFsgMTIsIDQgXSB9","dueTime":"","period":""}`,
		},
		`true`: {
			`{"data":true,"dueTime":"","period":""}`,
			`{"data":"dHJ1ZQ==","dueTime":"","period":""}`,
		},
		`null`: {
			`{"data":null,"dueTime":"","period":""}`,
			`{"data":"bnVsbA==","dueTime":"","period":""}`,
		},
		`[]`: {
			`{"data":[],"dueTime":"","period":""}`,
			`{"data":"W10=","dueTime":"","period":""}`,
		},
		`123`: {
			`{"data":123,"dueTime":"","period":""}`,
			`{"data":"MTIz","dueTime":"","period":""}`,
		},
	}

	var i atomic.Int64
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			actorID := strconv.FormatInt(i.Add(1), 10)
			h.lock.Lock()
			ch := make(chan string, 1)
			h.data[actorID] = ch
			h.lock.Unlock()

			body := `{"dueTime": "0s"`
			if name != `` {
				body += `,"data": ` + name
			}
			body += `}`
			aurl := h.daprd.ActorReminderURL("myactortype", actorID, "remindermethod-http")
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, aurl, strings.NewReader(body))
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			select {
			case <-time.After(time.Second * 10):
				require.FailNow(t, "timeout")
			case got := <-ch:
				assert.Equal(t, test.expHTTP, got, name)
			}

			_, err = gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
				ActorType: "myactortype",
				ActorId:   actorID,
				Name:      "remindermethod-grpc",
				DueTime:   "0s",
				Data:      []byte(name),
			})
			require.NoError(t, err)
			select {
			case <-time.After(time.Second * 10):
				require.FailNow(t, "timeout")
			case got := <-ch:
				assert.Equal(t, test.expGRPC, got, name)
			}
		})
	}
}
