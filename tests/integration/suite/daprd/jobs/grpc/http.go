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
	"io"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	dataCh    chan []byte
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.scheduler = scheduler.New(t)

	h.dataCh = make(chan []byte, 1)
	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			h.dataCh <- body
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(h.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(h.scheduler, app, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.scheduler.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	tests := map[string]struct {
		data func(t *testing.T) *anypb.Any
		exp  string
	}{
		"strings": {
			data: func(t *testing.T) *anypb.Any {
				anyB, err := anypb.New(wrapperspb.String("hello world"))
				require.NoError(t, err)
				return anyB
			},
			exp: "hello world",
		},
		"number": {
			data: func(t *testing.T) *anypb.Any {
				anyB, err := anypb.New(wrapperspb.Int64(123))
				require.NoError(t, err)
				return anyB
			},
			exp: "\b{",
		},
		"number-2": {
			data: func(t *testing.T) *anypb.Any {
				anyB, err := anypb.New(structpb.NewNumberValue(123))
				require.NoError(t, err)
				return anyB
			},
			exp: "123",
		},
		"expr": {
			data: func(*testing.T) *anypb.Any {
				return &anypb.Any{
					TypeUrl: "type.googleapis.com/google.type.Expr",
					Value:   []byte(`{"expression": "val"}`),
				}
			},
			exp: `{"expression": "val"}`,
		},
		"bytes": {
			data: func(t *testing.T) *anypb.Any {
				t.Helper()
				anyB, err := anypb.New(wrapperspb.Bytes([]byte("hello world")))
				require.NoError(t, err)
				return anyB
			},
			exp: "hello world",
		},
		"ping-garbage": {
			data: func(t *testing.T) *anypb.Any {
				t.Helper()
				anyB, err := anypb.New(&proto.PingResponse{Value: "pong", Counter: 123})
				require.NoError(t, err)
				return anyB
			},
			exp: "\x04pong\x10{",
		},
		"bool": {
			data: func(t *testing.T) *anypb.Any {
				t.Helper()
				anyB, err := anypb.New(structpb.NewBoolValue(true))
				require.NoError(t, err)
				return anyB
			},
			exp: "true",
		},
		"bool-string": {
			data: func(t *testing.T) *anypb.Any {
				t.Helper()
				anyB, err := anypb.New(structpb.NewStringValue("true"))
				require.NoError(t, err)
				return anyB
			},
			exp: `"true"`,
		},
	}

	client := h.daprd.GRPCClient(t, ctx)
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := client.ScheduleJobAlpha1(ctx, &runtimev1pb.ScheduleJobRequest{
				Job: &runtimev1pb.Job{
					Name:    name,
					DueTime: ptr.Of("0s"),
					Data:    test.data(t),
				},
			})
			require.NoError(t, err)

			select {
			case data := <-h.dataCh:
				assert.Equal(t, test.exp, string(data))
			case <-time.After(time.Second * 10):
				assert.Fail(t, "timed out waiting for triggered job")
			}
		})
	}
}
