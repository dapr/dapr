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

package retry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func init() {
	suite.Register(new(retryGRPCProxy))
}

type retryGRPCProxy struct {
	grpcResiliency string
	counters       sync.Map

	grpcProxyHandler grpcapp.Option
}

func (rt *retryGRPCProxy) Setup(t *testing.T) []framework.Option {
	rt.grpcResiliency = `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: grpcresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 1ms
        maxRetries: 2
        matching:
          gRPCStatusCodes: "%s"
`

	rt.grpcProxyHandler = grpcapp.WithPingFn(func(ctx context.Context, in *testpb.PingRequest) (*testpb.PingResponse, error) {
		data := in.GetValue()
		var message map[string]string

		err := json.Unmarshal([]byte(data), &message)
		require.NoError(t, err)
		if message["key"] == "" {
			return nil, errors.New("key is empty")
		}
		if message["statusCode"] == "" {
			return nil, errors.New("statusCode is empty")
		}

		key := message["key"]
		c, _ := rt.counters.LoadOrStore(key, &atomic.Int32{})
		counter := c.(*atomic.Int32)
		counter.Add(1)

		respStatusCode, err := strconv.Atoi(message["statusCode"])
		if err != nil {
			respStatusCode = 2 // Unknown
		}

		if respStatusCode == 0 {
			successResponse := &testpb.PingResponse{
				Value:   strconv.Itoa(respStatusCode),
				Counter: counter.Load(),
			}
			return successResponse, nil
		} else {
			// TODO: Update types to uint32
			//nolint:gosec
			return nil, status.Errorf(codes.Code(respStatusCode), "error for key: %s", key)
		}
	})

	return []framework.Option{}
}

func (rt *retryGRPCProxy) Run(t *testing.T, ctx context.Context) {
	scenarios := []grpcProxyTestScenario{
		{
			title:           "No status codes",
			statusCodes:     "",
			statusCodesTest: []int{1},
			expectRetries:   true,
		},
		{
			title:           "Single status code no retries",
			statusCodes:     "1",
			statusCodesTest: []int{2},
			expectRetries:   false,
		},
		{
			title:           "Single status code with retries",
			statusCodes:     "3",
			statusCodesTest: []int{3},
			expectRetries:   true,
		},
		{
			title:           "Multiple status codes no retries",
			statusCodes:     "1,12",
			statusCodesTest: []int{2, 11},
			expectRetries:   false,
		},
		{
			title:           "Multiple status codes with retries",
			statusCodes:     "5,12",
			statusCodesTest: []int{5, 12},
			expectRetries:   true,
		},
		{
			title:           "Range success status codes no retries",
			statusCodes:     "6-14",
			statusCodesTest: []int{0, 1, 2, 15},
			expectRetries:   false,
		},
		{
			title:           "Range status codes with retries",
			statusCodes:     "6-14",
			statusCodesTest: []int{6, 7, 8, 9, 10, 11, 12, 13, 14},
			expectRetries:   true,
		},
		{
			title:           "Multiple ranges status codes no retries",
			statusCodes:     "1,3-5,7",
			statusCodesTest: []int{0, 2, 6, 10},
			expectRetries:   false,
		},
		{
			title:           "Multiple ranges status codes with retries",
			statusCodes:     "1,3-5,7",
			statusCodesTest: []int{1, 3, 4, 5, 7},
			expectRetries:   true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.title, func(t *testing.T) {
			rt.runGrpcProxyScenario(t, ctx, scenario)
		})
	}
}

type grpcProxyTestScenario struct {
	title           string
	statusCodes     string
	expectRetries   bool
	statusCodesTest []int
}

func (rt *retryGRPCProxy) runGrpcProxyScenario(t *testing.T, ctx context.Context, scenario grpcProxyTestScenario) {
	app1 := grpcapp.New(t, rt.grpcProxyHandler)
	app1.Run(t, ctx)

	daprd1 := daprd.New(t,
		daprd.WithAppPort(app1.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.grpcResiliency, scenario.statusCodes)),
	)
	daprd1.Run(t, ctx)
	defer daprd1.Cleanup(t)

	app2 := grpcapp.New(t, rt.grpcProxyHandler)
	app2.Run(t, ctx)

	daprd2 := daprd.New(t,
		daprd.WithAppPort(app2.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.grpcResiliency, scenario.statusCodes)),
	)

	daprd2.Run(t, ctx)
	defer daprd2.Cleanup(t)

	daprd1.WaitUntilRunning(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)

	for _, statusCode := range scenario.statusCodesTest {
		key := uuid.NewString()
		statusCodeStr := strconv.Itoa(statusCode)

		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, daprd1.GRPCAddress(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		ctxWithMetadata := metadata.AppendToOutgoingContext(
			ctx, "dapr-app-id", daprd2.AppID())

		_, err = testpb.NewTestServiceClient(conn).
			Ping(ctxWithMetadata, &testpb.PingRequest{Value: fmt.Sprintf(`{"key": "%s", "statusCode": "%s"}`, key, statusCodeStr)})

		expectedCount := 1
		if scenario.expectRetries {
			// 3 = 1 try + 2 retries.
			expectedCount = 3
			require.Error(t, err)
		}

		assert.Equal(t, expectedCount, rt.getCount(key), "Retry count mismatch for test case '%s' with codes %s and test code %d", scenario.title, scenario.statusCodes, statusCode)
	}
}

func (rt *retryGRPCProxy) getCount(key string) int {
	c, ok := rt.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}
