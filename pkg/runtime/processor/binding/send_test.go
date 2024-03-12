/*
Copyright 2023 The Dapr Authors
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

package binding

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security"
	daprt "github.com/dapr/dapr/pkg/testing"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

func TestIsBindingOfExplicitDirection(t *testing.T) {
	t.Run("no direction in metadata input binding", func(t *testing.T) {
		m := map[string]string{}
		r := isBindingOfExplicitDirection("input", m)

		assert.False(t, r)
	})

	t.Run("no direction in metadata output binding", func(t *testing.T) {
		m := map[string]string{}
		r := isBindingOfExplicitDirection("input", m)

		assert.False(t, r)
	})

	t.Run("direction is input binding", func(t *testing.T) {
		m := map[string]string{
			"direction": "input",
		}
		r := isBindingOfExplicitDirection("input", m)

		assert.True(t, r)
	})

	t.Run("direction is output binding", func(t *testing.T) {
		m := map[string]string{
			"direction": "output",
		}
		r := isBindingOfExplicitDirection("output", m)

		assert.True(t, r)
	})

	t.Run("direction is not output binding", func(t *testing.T) {
		m := map[string]string{
			"direction": "input",
		}
		r := isBindingOfExplicitDirection("output", m)

		assert.False(t, r)
	})

	t.Run("direction is not input binding", func(t *testing.T) {
		m := map[string]string{
			"direction": "output",
		}
		r := isBindingOfExplicitDirection("input", m)

		assert.False(t, r)
	})

	t.Run("direction is both input and output binding", func(t *testing.T) {
		m := map[string]string{
			"direction": "output, input",
		}

		r := isBindingOfExplicitDirection("input", m)
		assert.True(t, r)

		r2 := isBindingOfExplicitDirection("output", m)

		assert.True(t, r2)
	})
}

func TestStartReadingFromBindings(t *testing.T) {
	t.Run("OPTIONS request when direction is not specified", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)

		m := &rtmock.Binding{}

		b.compStore.AddInputBinding("test", m)
		err := b.StartReadingFromBindings(context.Background())

		require.NoError(t, err)
		assert.True(t, mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything))
	})

	t.Run("No OPTIONS request when direction is specified", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)

		m := &rtmock.Binding{
			Metadata: map[string]string{
				"direction": "input",
			},
		}

		b.compStore.AddInputBinding("test", m)
		require.NoError(t, b.compStore.AddPendingComponentForCommit(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type: "bindings.test",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "direction",
						Value: commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("input")},
						},
					},
				},
			},
		}))
		require.NoError(t, b.compStore.CommitPendingComponent())
		err := b.StartReadingFromBindings(context.Background())
		require.NoError(t, err)
		assert.True(t, mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything))
	})
}

func TestGetSubscribedBindingsGRPC(t *testing.T) {
	secP, err := security.New(context.Background(), security.Options{
		TrustAnchors:            []byte("test"),
		AppID:                   "test",
		ControlPlaneTrustDomain: "test.example.com",
		ControlPlaneNamespace:   "default",
		MTLSEnabled:             false,
		OverrideCertRequestSource: func(context.Context, []byte) ([]*x509.Certificate, error) {
			return []*x509.Certificate{nil}, nil
		},
	})
	require.NoError(t, err)
	go secP.Run(context.Background())
	sec, err := secP.Handler(context.Background())
	require.NoError(t, err)

	testCases := []struct {
		name             string
		expectedResponse []string
		responseError    error
		responseFromApp  []string
	}{
		{
			name:             "get list of subscriber bindings success",
			expectedResponse: []string{"binding1", "binding2"},
			responseFromApp:  []string{"binding1", "binding2"},
		},
		{
			name:             "get list of subscriber bindings error from app",
			expectedResponse: []string{},
			responseError:    assert.AnError,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			port, _ := freeport.GetFreePort()
			b := New(Options{
				IsHTTP:         false,
				Resiliency:     resiliency.New(log),
				ComponentStore: compstore.New(),
				Meta:           meta.New(meta.Options{}),
				GRPC:           manager.NewManager(sec, modes.StandaloneMode, &manager.AppChannelConfig{Port: port}),
			})
			// create mock application server first
			grpcServer := testinggrpc.StartTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
				Error:    tc.responseError,
				Bindings: tc.responseFromApp,
			})
			defer grpcServer.Stop()
			// act
			resp, _ := b.getSubscribedBindingsGRPC(context.Background())

			// assert
			assert.Equal(t, tc.expectedResponse, resp, "expected response to match")
		})
	}
}

func TestReadInputBindings(t *testing.T) {
	const testInputBindingName = "inputbinding"
	const testInputBindingMethod = "inputbinding"

	t.Run("app acknowledge, no retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(rtmock.TestInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), matchDaprRequestMethod(testInputBindingMethod)).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), fakeReq).Return(fakeResp, nil)

		b.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		mockBinding := rtmock.Binding{}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		mockBinding.ReadErrorCh = ch
		b.readFromBinding(ctx, testInputBindingName, &mockBinding)
		cancel()

		assert.False(t, <-ch)
	})

	t.Run("app returns error", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer fakeBindingReq.Close()

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(rtmock.TestInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil).
			WithRawDataString("Internal Error").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), fakeBindingReq).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), fakeReq).Return(fakeResp, nil)

		b.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		mockBinding := rtmock.Binding{}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		mockBinding.ReadErrorCh = ch
		b.readFromBinding(ctx, testInputBindingName, &mockBinding)
		cancel()

		assert.True(t, <-ch)
	})

	t.Run("binding has data and metadata", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer fakeBindingReq.Close()

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(rtmock.TestInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{"bindings": {"input"}})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), matchDaprRequestMethod(testInputBindingMethod)).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), fakeReq).Return(fakeResp, nil)

		b.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		mockBinding := rtmock.Binding{Metadata: map[string]string{"bindings": "input"}}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		mockBinding.ReadErrorCh = ch
		b.readFromBinding(ctx, testInputBindingName, &mockBinding)
		cancel()

		assert.Equal(t, string(rtmock.TestInputBindingData), mockBinding.Data)
	})

	t.Run("start and stop reading", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		closeCh := make(chan struct{})
		defer close(closeCh)

		mockBinding := &daprt.MockBinding{}
		mockBinding.SetOnReadCloseCh(closeCh)
		mockBinding.On("Read", mock.MatchedBy(daprt.MatchContextInterface), mock.Anything).Return(nil).Once()

		ctx, cancel := context.WithCancel(context.Background())
		b.readFromBinding(ctx, testInputBindingName, mockBinding)
		time.Sleep(80 * time.Millisecond)
		cancel()
		select {
		case <-closeCh:
			// All good
		case <-time.After(time.Second):
			t.Fatal("timeout while waiting for binding to stop reading")
		}

		mockBinding.AssertNumberOfCalls(t, "Read", 1)
	})
}

func TestInvokeOutputBindings(t *testing.T) {
	t.Run("output binding missing operation", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		_, err := b.SendToOutputBinding(context.Background(), "mockBinding", &bindings.InvokeRequest{
			Data: []byte(""),
		})
		require.Error(t, err)
		assert.Equal(t, "operation field is missing from request", err.Error())
	})

	t.Run("output binding valid operation", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		b.compStore.AddOutputBinding("mockBinding", &rtmock.Binding{})

		_, err := b.SendToOutputBinding(context.Background(), "mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.CreateOperation,
		})
		require.NoError(t, err)
	})

	t.Run("output binding invalid operation", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		b := New(Options{
			IsHTTP:         true,
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		b.compStore.AddOutputBinding("mockBinding", &rtmock.Binding{})

		_, err := b.SendToOutputBinding(context.Background(), "mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.GetOperation,
		})
		require.Error(t, err)
		assert.Equal(t, "binding mockBinding does not support operation get. supported operations:create list", err.Error())
	})
}

func TestBindingTracingHttp(t *testing.T) {
	b := New(Options{
		IsHTTP:         true,
		Resiliency:     resiliency.New(log),
		ComponentStore: compstore.New(),
		Meta:           meta.New(meta.Options{}),
	})

	t.Run("traceparent passed through with response status code 200", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		_, err := b.sendBindingEventToApp(context.Background(), "mockBinding", []byte(""), map[string]string{"traceparent": "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01"})
		require.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
		req := mockAppChannel.Calls[0].Arguments.Get(1).(*invokev1.InvokeMethodRequest)
		assert.Contains(t, req.Metadata(), "traceparent")
		assert.Contains(t, req.Metadata()["traceparent"].GetValues(), "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01")
	})

	t.Run("traceparent passed through with response status code 204", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(204, "OK", nil), nil)
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		_, err := b.sendBindingEventToApp(context.Background(), "mockBinding", []byte(""), map[string]string{"traceparent": "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01"})
		require.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
		req := mockAppChannel.Calls[0].Arguments.Get(1).(*invokev1.InvokeMethodRequest)
		assert.Contains(t, req.Metadata(), "traceparent")
		assert.Contains(t, req.Metadata()["traceparent"].GetValues(), "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01")
	})

	t.Run("bad traceparent does not fail request", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		b.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		_, err := b.sendBindingEventToApp(context.Background(), "mockBinding", []byte(""), map[string]string{"traceparent": "I am not a traceparent"})
		require.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
	})
}

func TestBindingResiliency(t *testing.T) {
	b := New(Options{
		Resiliency:     resiliency.FromConfigurations(logger.NewLogger("test"), daprt.TestResiliency),
		Registry:       registry.New(registry.NewOptions()).Bindings(),
		ComponentStore: compstore.New(),
		Meta:           meta.New(meta.Options{}),
	})

	failingChannel := daprt.FailingAppChannel{
		Failure: daprt.NewFailure(
			map[string]int{
				"inputFailingKey": 1,
			},
			map[string]time.Duration{
				"inputTimeoutKey": time.Second * 10,
			},
			map[string]int{},
		),
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			r, _ := io.ReadAll(req.RawData())
			return string(r)
		},
	}

	b.channels = new(channels.Channels).WithAppChannel(&failingChannel)
	b.isHTTP = true

	failingBinding := daprt.FailingBinding{
		Failure: daprt.NewFailure(
			map[string]int{
				"outputFailingKey": 1,
			},
			map[string]time.Duration{
				"outputTimeoutKey": time.Second * 10,
			},
			map[string]int{},
		),
	}

	b.registry.RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &failingBinding
		},
		"failingoutput",
	)

	output := componentsV1alpha1.Component{}
	output.ObjectMeta.Name = "failOutput"
	output.Spec.Type = "bindings.failingoutput"
	err := b.Init(context.TODO(), output)
	require.NoError(t, err)

	t.Run("output binding retries on failure with resiliency", func(t *testing.T) {
		req := &bindings.InvokeRequest{
			Data:      []byte("outputFailingKey"),
			Operation: "create",
		}
		_, err := b.SendToOutputBinding(context.Background(), "failOutput", req)

		require.NoError(t, err)
		assert.Equal(t, 2, failingBinding.Failure.CallCount("outputFailingKey"))
	})

	t.Run("output binding times out with resiliency", func(t *testing.T) {
		req := &bindings.InvokeRequest{
			Data:      []byte("outputTimeoutKey"),
			Operation: "create",
		}
		start := time.Now()
		_, err := b.SendToOutputBinding(context.Background(), "failOutput", req)
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingBinding.Failure.CallCount("outputTimeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("input binding retries on failure with resiliency", func(t *testing.T) {
		_, err := b.sendBindingEventToApp(context.Background(), "failingInputBinding", []byte("inputFailingKey"), map[string]string{})

		require.NoError(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount("inputFailingKey"))
	})

	t.Run("input binding times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := b.sendBindingEventToApp(context.Background(), "failingInputBinding", []byte("inputTimeoutKey"), map[string]string{})
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount("inputTimeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

func matchDaprRequestMethod(method string) any {
	return mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
		if req == nil || req.Message() == nil || req.Message().GetMethod() != method {
			return false
		}
		return true
	})
}
