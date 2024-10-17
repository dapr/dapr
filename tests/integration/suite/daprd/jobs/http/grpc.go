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

package http

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.scheduler = scheduler.New(t)

	g.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			g.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	g.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(g.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(g.scheduler, srv, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.scheduler.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	tests := map[string]struct {
		data any
		exp  func(*testing.T) *runtimev1pb.JobEventRequest
	}{
		"simple-string": {
			data: `"someData"`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				data, err := anypb.New(structpb.NewStringValue("someData"))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "simple-string",
					Method:        "job/simple-string",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
		"quoted-string": {
			data: `"\"xyz\""`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				data, err := anypb.New(structpb.NewStringValue(`"xyz"`))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "quoted-string",
					Method:        "job/quoted-string",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
		"number": {
			data: `123`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				data, err := anypb.New(structpb.NewNumberValue(123))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "number",
					Method:        "job/number",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
		"number-quoted": {
			data: `"123"`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				data, err := anypb.New(structpb.NewStringValue(`123`))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "number-quoted",
					Method:        "job/number-quoted",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
		"object": {
			data: `{"expression":"val"}`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				str, err := structpb.NewStruct(map[string]any{
					"expression": "val",
				})
				require.NoError(t, err)
				data, err := anypb.New(structpb.NewStructValue(str))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "object",
					Method:        "job/object",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
		"object-space": {
			data: `{        "xyz": 123   }`,
			exp: func(t *testing.T) *runtimev1pb.JobEventRequest {
				t.Helper()
				str, err := structpb.NewStruct(map[string]any{
					"xyz": 123,
				})
				require.NoError(t, err)
				data, err := anypb.New(structpb.NewStructValue(str))
				require.NoError(t, err)
				return &runtimev1pb.JobEventRequest{
					Name:          "object-space",
					Method:        "job/object-space",
					Data:          data,
					ContentType:   "type.googleapis.com/google.protobuf.Value",
					HttpExtension: &commonv1pb.HTTPExtension{Verb: commonv1pb.HTTPExtension_POST},
				}
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Helper()

			body := strings.NewReader(fmt.Sprintf(`{"dueTime":"0s","data":%s}`, test.data))
			g.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/"+name, body)
			select {
			case job := <-g.jobChan:
				//nolint:protogetter
				assert.Equal(t, test.exp(t), &runtimev1pb.JobEventRequest{
					Name:          job.Name,
					Method:        job.Method,
					ContentType:   job.ContentType,
					HttpExtension: job.HttpExtension,
					Data:          job.Data,
				})
			case <-time.After(time.Second * 10):
				assert.Fail(t, "timed out waiting for triggered job")
			}
		})
	}
}
