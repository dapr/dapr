package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	httpapp "github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	suite.Register(new(retry))
}

type retry struct {
	retries        int
	httpResiliency string
	grpcResiliency string
	counters       sync.Map

	handlerFuncRoot     httpapp.Option
	handlerFuncRetry    httpapp.Option
	grpcOnInvokeHandler grpcapp.Option
	grpcProxyHandler    grpcapp.Option
}

func (rt *retry) Setup(t *testing.T) []framework.Option {
	rt.httpResiliency = `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: httpresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 1ms
        maxRetries: 2
        matching:
          httpStatusCodes: "%s"
`
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

	rt.handlerFuncRoot = httpapp.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})

	rt.handlerFuncRetry = httpapp.WithHandlerFunc("/retry", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		c, _ := rt.counters.LoadOrStore(key, &atomic.Int32{})
		counter := c.(*atomic.Int32)
		counter.Add(1)

		respStatusCode, err := strconv.Atoi(string(body))
		if err != nil {
			respStatusCode = 204
		}

		w.Header().Set("content-type", "text/plain")
		w.WriteHeader(respStatusCode)
		w.Write([]byte{})
	})

	rt.grpcOnInvokeHandler = grpcapp.WithOnInvokeFn(func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		data := in.GetData()

		var message map[string]string

		err := json.Unmarshal(data.GetValue(), &message)
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
			successResponse := &commonv1.InvokeResponse{
				Data:        data,
				ContentType: in.GetContentType(),
			}
			return successResponse, nil
		} else {
			// TODO: Update types to uint32
			//nolint:gosec
			return nil, status.Errorf(codes.Code(respStatusCode), "error for key: %s", key)
		}
	})

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

func (rt *retry) Run(t *testing.T, ctx context.Context) {
	scenarios := []testScenario{
		{
			title:           "No status codes",
			protocol:        "http",
			statusCodes:     "",
			statusCodesTest: []int{500},
			expectRetries:   true,
		},
		{
			title:           "Single status code no retries",
			protocol:        "http",
			statusCodes:     "200",
			statusCodesTest: []int{200},
			expectRetries:   false,
		},
		{
			title:           "Single status code with retries",
			protocol:        "http",
			statusCodes:     "500",
			statusCodesTest: []int{500},
			expectRetries:   true,
		},
		{
			title:           "Multiple status codes no retries",
			protocol:        "http",
			statusCodes:     "200,301",
			statusCodesTest: []int{200, 301},
			expectRetries:   false,
		},
		{
			title:           "Multiple status codes with retries",
			protocol:        "http",
			statusCodes:     "400,500",
			statusCodesTest: []int{400, 500},
			expectRetries:   true,
		},
		{
			title:           "Range success status codes no retries",
			protocol:        "http",
			statusCodes:     "200-320",
			statusCodesTest: []int{201, 301},
			expectRetries:   false,
		},
		{
			title:           "Range status codes with retries",
			protocol:        "http",
			statusCodes:     "400-550",
			statusCodesTest: []int{404, 501},
			expectRetries:   true,
		},
		{
			title:           "Multiple ranges status codes no retries",
			protocol:        "http",
			statusCodes:     "200,400-500,501",
			statusCodesTest: []int{200, 301, 399, 502},
			expectRetries:   false,
		},
		{
			title:           "Multiple ranges status codes with retries",
			protocol:        "http",
			statusCodes:     "200,400-500,501",
			statusCodesTest: []int{400, 401, 420, 500, 501},
			expectRetries:   true,
		},
		{
			title:           "No status codes",
			protocol:        "grpc",
			statusCodes:     "",
			statusCodesTest: []int{1},
			expectRetries:   true,
		},
		{
			title:           "Single status code no retries",
			protocol:        "grpc",
			statusCodes:     "1",
			statusCodesTest: []int{2},
			expectRetries:   false,
		},
		{
			title:           "Single status code with retries",
			protocol:        "grpc",
			statusCodes:     "3",
			statusCodesTest: []int{3},
			expectRetries:   true,
		},
		{
			title:           "Multiple status codes no retries",
			protocol:        "grpc",
			statusCodes:     "1,12",
			statusCodesTest: []int{2, 11},
			expectRetries:   false,
		},
		{
			title:           "Multiple status codes with retries",
			protocol:        "grpc",
			statusCodes:     "5,12",
			statusCodesTest: []int{5, 12},
			expectRetries:   true,
		},
		{
			title:           "Range success status codes no retries",
			protocol:        "grpc",
			statusCodes:     "6-14",
			statusCodesTest: []int{0, 1, 2, 15},
			expectRetries:   false,
		},
		{
			title:           "Range status codes with retries",
			protocol:        "grpc",
			statusCodes:     "6-14",
			statusCodesTest: []int{6, 7, 8, 9, 10, 11, 12, 13, 14},
			expectRetries:   true,
		},
		{
			title:           "Multiple ranges status codes no retries",
			protocol:        "grpc",
			statusCodes:     "1,3-5,7",
			statusCodesTest: []int{0, 2, 6, 10},
			expectRetries:   false,
		},
		{
			title:           "Multiple ranges status codes with retries",
			protocol:        "grpc",
			statusCodes:     "1,3-5,7",
			statusCodesTest: []int{1, 3, 4, 5, 7},
			expectRetries:   true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(strings.ToUpper(scenario.protocol)+" - "+scenario.title, func(t *testing.T) {
			if scenario.protocol == "http" {
				rt.runHTTPScenario(t, ctx, scenario)
			}
			if scenario.protocol == "grpc" {
				rt.runGrpcScenario(t, ctx, scenario)
				rt.runGrpcProxyScenario(t, ctx, scenario)
			}
		})
	}
}

type testScenario struct {
	title           string
	statusCodes     string
	expectRetries   bool
	protocol        string
	statusCodesTest []int
}

func (rt *retry) runGrpcScenario(t *testing.T, ctx context.Context, scenario testScenario) {
	app1 := grpcapp.New(t, rt.grpcOnInvokeHandler, rt.grpcProxyHandler)
	app1.Run(t, ctx)

	daprd1 := daprd.New(t,
		daprd.WithAppPort(app1.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.grpcResiliency, scenario.statusCodes)),
	)
	daprd1.Run(t, ctx)
	defer daprd1.Cleanup(t)

	app2 := grpcapp.New(t, rt.grpcOnInvokeHandler)
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
		rt.retries = 0

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

		_, err = rtv1.NewAppCallbackClient(conn).OnInvoke(ctxWithMetadata, &commonv1.InvokeRequest{
			Method: key,
			Data:   &anypb.Any{Value: []byte(fmt.Sprintf(`{"key": "%s", "statusCode": "%s"}`, key, statusCodeStr))},
		})

		expectedCount := 1
		if scenario.expectRetries {
			// 3 = 1 try + 2 retries.
			expectedCount = 3
			require.Error(t, err)
		}

		assert.Equal(t, expectedCount, rt.getCount(key), "Retry count mismatch for test case '%s' with codes %s and test code %d", scenario.title, scenario.statusCodes, statusCode)
	}
}

func (rt *retry) runGrpcProxyScenario(t *testing.T, ctx context.Context, scenario testScenario) {
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
		rt.retries = 0

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

func (rt *retry) runHTTPScenario(t *testing.T, ctx context.Context, scenario testScenario) {
	app1 := httpapp.New(t, rt.handlerFuncRoot, rt.handlerFuncRetry)
	app1.Run(t, ctx)

	daprd1 := daprd.New(t,
		daprd.WithAppPort(app1.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.httpResiliency, scenario.statusCodes)),
	)
	daprd1.Run(t, ctx)
	defer daprd1.Cleanup(t)

	app2 := httpapp.New(t, rt.handlerFuncRoot, rt.handlerFuncRetry)
	app2.Run(t, ctx)

	daprd2 := daprd.New(t,
		daprd.WithAppPort(app2.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(fmt.Sprintf(rt.httpResiliency, scenario.statusCodes)),
	)
	daprd2.Run(t, ctx)
	defer daprd2.Cleanup(t)

	daprd1.WaitUntilRunning(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)

	for _, statusCode := range scenario.statusCodesTest {
		rt.retries = 0

		key := uuid.NewString()
		statusCodeStr := strconv.Itoa(statusCode)

		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/retry?key=%s", daprd1.HTTPPort(), daprd2.AppID(), key)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(statusCodeStr))
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		expectedCount := 1
		if scenario.expectRetries {
			// 3 = 1 try + 2 retries.
			expectedCount = 3
		}
		assert.Equal(t, expectedCount, rt.getCount(key), "Retry count mismatch for test case '%s' with codes %s and test code %d", scenario.title, scenario.statusCodes, statusCode)
	}
}

func (rt *retry) getCount(key string) int {
	c, ok := rt.counters.Load(key)
	if !ok {
		return 0
	}

	return int(c.(*atomic.Int32).Load())
}
