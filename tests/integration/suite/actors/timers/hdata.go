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

package timers

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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

	lock sync.Mutex
	data map[string]chan string
}

func (h *hdata) Setup(t *testing.T) []framework.Option {
	h.data = make(map[string]chan string)

	handler := nethttp.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/{id}", func(w nethttp.ResponseWriter, r *nethttp.Request) {
	})
	handler.HandleFunc("/actors/myactortype/{id}/method/timer/timermethod", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		b, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		h.lock.Lock()
		ch := h.data[r.PathValue("id")]
		h.lock.Unlock()
		ch <- string(b)
	})
	handler.HandleFunc("/actors/myactortype/{id}/method/foo", func(w nethttp.ResponseWriter, r *nethttp.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	sched := scheduler.New(t)
	h.place = placement.New(t)
	h.daprd = daprd.New(t,
		daprd.WithScheduler(sched),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, h.place, srv, h.daprd),
	}
}

func (h *hdata) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	gclient := h.daprd.GRPCClient(t, ctx)

	tests := map[string]struct {
		expHTTP string
		expGRPC string
	}{
		``: {
			`{"callback":"","dueTime":"0s","period":""}`,
			`{"callback":"","dueTime":"0s","period":""}`,
		},
		`"foo"`: {
			`{"data":"foo","callback":"","dueTime":"0s","period":""}`,
			`{"data":"ImZvbyI=","callback":"","dueTime":"0s","period":""}`,
		},
		`{  "foo": [ 12, 4 ] }`: {
			`{"data":{"foo":[12,4]},"callback":"","dueTime":"0s","period":""}`,
			`{"data":"eyAgImZvbyI6IFsgMTIsIDQgXSB9","callback":"","dueTime":"0s","period":""}`,
		},
		`true`: {
			`{"data":true,"callback":"","dueTime":"0s","period":""}`,
			`{"data":"dHJ1ZQ==","callback":"","dueTime":"0s","period":""}`,
		},
		`null`: {
			`{"data":null,"callback":"","dueTime":"0s","period":""}`,
			`{"data":"bnVsbA==","callback":"","dueTime":"0s","period":""}`,
		},
		`[]`: {
			`{"data":[],"callback":"","dueTime":"0s","period":""}`,
			`{"data":"W10=","callback":"","dueTime":"0s","period":""}`,
		},
		`123`: {
			`{"data":123,"callback":"","dueTime":"0s","period":""}`,
			`{"data":"MTIz","callback":"","dueTime":"0s","period":""}`,
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
			aurl := fmt.Sprintf("http://%s/v1.0/actors/myactortype/%s/timers/timermethod", h.daprd.HTTPAddress(), actorID)
			req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, aurl, strings.NewReader(body))
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
			assert.Equal(t, test.expHTTP, <-ch, name)

			_, err = gclient.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
				ActorType: "myactortype",
				ActorId:   actorID,
				Name:      "timermethod",
				DueTime:   "0s",
				Data:      []byte(name),
			})
			require.NoError(t, err)
			assert.Equal(t, test.expGRPC, <-ch, name)
		})
	}
}
