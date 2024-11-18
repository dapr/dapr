/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apps

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retrymatchinggrpc))
}

type retrymatchinggrpc struct {
	daprd1   *daprd.Daprd
	daprd2   *daprd.Daprd
	counters sync.Map
}

func (d *retrymatchinggrpc) Setup(t *testing.T) []framework.Option {
	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		key := in.GetMethod()
		if key == "" {
			return nil, status.Error(codes.InvalidArgument, "key is empty")
		}

		dataStr := string(in.GetData().GetValue())
		responseStatusCode, err := strconv.Atoi(dataStr)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		c, _ := d.counters.LoadOrStore(key, &atomic.Int32{})
		counter := c.(*atomic.Int32)
		counter.Add(1)

		// TODO: Update types to uint32
		//nolint:gosec
		return nil, status.Error(codes.Code(responseStatusCode), "error for key: "+key)
	}

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 10ms
        maxRetries: 3
        matching:
          gRPCStatusCodes: 1,2,5-7
          httpStatusCodes: 500-599
`
	srv1 := app.New(t, app.WithOnInvokeFn(onInvoke))
	srv2 := app.New(t, app.WithOnInvokeFn(onInvoke))

	d.daprd1 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv1.Port(t)),
		daprd.WithResourceFiles(resiliency),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv2.Port(t)),
		daprd.WithResourceFiles(resiliency),
	)

	d.counters = sync.Map{}

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, d.daprd1, d.daprd2),
	}
}

func (d *retrymatchinggrpc) getCount(key string) int {
	c, ok := d.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}

func (d *retrymatchinggrpc) Run(t *testing.T, ctx context.Context) {
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	scenarios := []struct {
		title              string
		statusCode         int
		expectedStatusCode int
		expectRetries      bool
	}{
		{
			title:              "success not in matching list 0",
			statusCode:         0,
			expectedStatusCode: 0,
			expectRetries:      false,
		},
		{
			title:              "error code not in matching list 14",
			statusCode:         14,
			expectedStatusCode: 14,
			expectRetries:      false,
		},
		{
			title:              "error code in matching list 1",
			statusCode:         1,
			expectedStatusCode: 1,
			expectRetries:      true,
		},
		{
			title:              "invalid status code 20",
			statusCode:         20,
			expectedStatusCode: 20,
			expectRetries:      false,
		},
	}

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, d.daprd1.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			key := uuid.NewString()
			statusCodeStr := strconv.Itoa(scenario.statusCode)
			ctxWithMetadata := metadata.AppendToOutgoingContext(
				ctx, "dapr-app-id", d.daprd2.AppID())

			// Uses AppCallback API since it is already present in the codebase.
			// This will make the sidecar use the gRPC proxy way, it just happens
			// to be the AppCallback API (could be MyMadeUpService) but it does not
			// make a difference here.
			_, err := rtv1.NewAppCallbackClient(conn).OnInvoke(ctxWithMetadata, &commonv1.InvokeRequest{
				Method: key,
				Data:   &anypb.Any{Value: []byte(statusCodeStr)},
			})
			if scenario.statusCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			if e, ok := status.FromError(err); ok {
				// TODO: Update types to uint32
				//nolint:gosec
				require.Equal(t, codes.Code(scenario.expectedStatusCode), e.Code())
			} else {
				t.Fail()
			}

			expectedCount := 1
			if scenario.expectRetries {
				// 4 = 1 try + 3 retries.
				expectedCount = 4
			}
			assert.Equal(t, expectedCount, d.getCount(key))
		})
	}
}
