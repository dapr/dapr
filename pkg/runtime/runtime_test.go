// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"contrib.go.opencensus.io/exporter/zipkin"
	"github.com/dapr/components-contrib/bindings"
	comp_exporters "github.com/dapr/components-contrib/exporters"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/dapr/pkg/components/exporters"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/pkg/sentry/certs"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/ghodss/yaml"
	jsoniter "github.com/json-iterator/go"
	"github.com/phayes/freeport"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestRuntimeConfigID  = "consumer0"
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	maxGRPCServerUptime  = 200 * time.Millisecond
)

var (
	testCertRoot = `-----BEGIN CERTIFICATE-----
MIIBjjCCATOgAwIBAgIQdZeGNuAHZhXSmb37Pnx2QzAKBggqhkjOPQQDAjAYMRYw
FAYDVQQDEw1jbHVzdGVyLmxvY2FsMB4XDTIwMDIwMTAwMzUzNFoXDTMwMDEyOTAw
MzUzNFowGDEWMBQGA1UEAxMNY2x1c3Rlci5sb2NhbDBZMBMGByqGSM49AgEGCCqG
SM49AwEHA0IABAeMFRst4JhcFpebfgEs1MvJdD7h5QkCbLwChRHVEUoaDqd1aYjm
bX5SuNBXz5TBEhHfTV3Objh6LQ2N+CBoCeOjXzBdMA4GA1UdDwEB/wQEAwIBBjAS
BgNVHRMBAf8ECDAGAQH/AgEBMB0GA1UdDgQWBBRBWthv5ZQ3vALl2zXWwAXSmZ+m
qTAYBgNVHREEETAPgg1jbHVzdGVyLmxvY2FsMAoGCCqGSM49BAMCA0kAMEYCIQDN
rQNOck4ENOhmLROE/wqH0MKGjE6P8yzesgnp9fQI3AIhAJaVPrZloxl1dWCgmNWo
Iklq0JnMgJU7nS+VpVvlgBN8
-----END CERTIFICATE-----`

	testInputBindingData = []byte("fakedata")
)

type MockKubernetesStateStore struct {
	callback func()
}

func (m *MockKubernetesStateStore) Init(metadata secretstores.Metadata) error {
	if m.callback != nil {
		m.callback()
	}
	return nil
}

func (m *MockKubernetesStateStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (m *MockKubernetesStateStore) BulkGetSecret(req secretstores.BulkGetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func NewMockKubernetesStoreWithInitCallback(cb func()) secretstores.SecretStore {
	return &MockKubernetesStateStore{callback: cb}
}

func TestNewRuntime(t *testing.T) {
	// act
	r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{})

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

// helper to populate subscription array for 2 pubsubs.
// 'topics' are the topics for the first pubsub.
// 'topics2' are the topics for the second pubsub.
func getSubscriptionsJSONString(topics []string, topics2 []string) string {
	s := []runtime_pubsub.Subscription{}
	for _, t := range topics {
		s = append(s, runtime_pubsub.Subscription{
			PubsubName: TestPubsubName,
			Topic:      t,
			Route:      t,
		})
	}

	for _, t := range topics2 {
		s = append(s, runtime_pubsub.Subscription{
			PubsubName: TestSecondPubsubName,
			Topic:      t,
			Route:      t,
		})
	}
	b, _ := json.Marshal(&s)

	return string(b)
}

func getSubscriptionCustom(topic, route string) string {
	s := []runtime_pubsub.Subscription{
		{
			PubsubName: TestPubsubName,
			Topic:      topic,
			Route:      route,
		},
	}
	b, _ := json.Marshal(&s)
	return string(b)
}

func testDeclarativeSubscription() subscriptionsapi.Subscription {
	return subscriptionsapi.Subscription{
		TypeMeta: meta_v1.TypeMeta{
			Kind: "Subscription",
		},
		Spec: subscriptionsapi.SubscriptionSpec{
			Topic:      "topic1",
			Route:      "myroute",
			Pubsubname: "pubsub",
		},
	}
}

func writeSubscriptionToDisk(subscription subscriptionsapi.Subscription, filePath string) {
	b, _ := yaml.Marshal(subscription)
	ioutil.WriteFile(filePath, b, 0600)
}

func TestProcessComponentsAndDependents(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	incorrectComponentType := components_v1alpha1.Component{

		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsubs.mockPubSub",
			Metadata: getFakeMetadataItems(),
		},
	}

	t.Run("test incorrect type", func(t *testing.T) {
		err := rt.processComponentAndDependents(incorrectComponentType)
		assert.Error(t, err, "expected an error")
		assert.Equal(t, "incorrect type pubsubs.mockPubSub", err.Error(), "expected error strings to match")
	})
}

func TestDoProcessComponent(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	pubsubComponent := components_v1alpha1.Component{

		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Metadata: getFakeMetadataItems(),
		},
	}

	exportersComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "testexporter",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "exporters.mockExporter",
			Metadata: getFakeMetadataItems(),
		},
	}

	t.Run("test error on pubsub init", func(t *testing.T) {
		// setup
		mockPubSub := new(daprt.MockPubSub)

		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return mockPubSub
			}),
		)
		expectedMetadata := pubsub.Metadata{
			Properties: getFakeProperties(),
		}

		mockPubSub.On("Init", expectedMetadata).Return(assert.AnError)

		// act
		err := rt.doProcessOneComponent(ComponentCategory("pubsub"), pubsubComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, assert.AnError.Error(), err.Error(), "expected error strings to match")
	})

	t.Run("test error on exporter init", func(t *testing.T) {
		// setup
		mockExporter := new(daprt.MockExporter)

		rt.exporterRegistry.Register(
			exporters.New("mockExporter", func() comp_exporters.Exporter {
				return mockExporter
			}),
		)
		mockExporter.On("Init", TestRuntimeConfigID, "", comp_exporters.Metadata{
			Properties: getFakeProperties(),
			Buffer:     nil,
		}).Return(assert.AnError)

		// act
		err := rt.doProcessOneComponent(exporterComponent, exportersComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, assert.AnError.Error(), err.Error(), "expected error strings to match")
	})

	t.Run("test invalid category componen", func(t *testing.T) {
		// act
		err := rt.doProcessOneComponent(ComponentCategory("invalid"), pubsubComponent)

		// assert
		assert.NoError(t, err, "no error expected")
	})
}

func TestInitState(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	mockStateComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type: "state.mockState",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "actorStateStore",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{Raw: []byte("true")},
					},
				},
			},
		},
	}

	initMockStateStoreForRuntime := func(rt *DaprRuntime, e error) *daprt.MockStateStore {
		mockStateStore := new(daprt.MockStateStore)

		rt.stateStoreRegistry.Register(
			state_loader.New("mockState", func() state.Store {
				return mockStateStore
			}),
		)

		expectedMetadata := state.Metadata{
			Properties: map[string]string{
				actorStateStore: "true",
			},
		}

		mockStateStore.On("Init", expectedMetadata).Return(e)

		return mockStateStore
	}

	t.Run("test init state store", func(t *testing.T) {
		// setup
		initMockStateStoreForRuntime(rt, nil)

		// act
		err := rt.initState(mockStateComponent)

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init state store error", func(t *testing.T) {
		// setup
		initMockStateStoreForRuntime(rt, assert.AnError)

		// act
		err := rt.initState(mockStateComponent)

		// assert
		assert.Error(t, err, "expected error")
		assert.Equal(t, assert.AnError.Error(), err.Error(), "expected error strings to match")
	})
}

func TestSetupTracing(t *testing.T) {
	testcases := []struct {
		name              string
		tracingConfig     config.TracingSpec
		hostAddress       string
		expectedExporters []trace.Exporter
		expectedErr       string
	}{{
		name:          "no trace exporter",
		tracingConfig: config.TracingSpec{},
	}, {
		name:        "bad host address, failing zipkin",
		hostAddress: "bad:host:address",
		tracingConfig: config.TracingSpec{
			Zipkin: config.ZipkinSpec{
				EndpointAddress: "http://foo.bar",
			},
		},
		expectedErr: "too many colons",
	}, {
		name: "zipkin trace exporter",
		tracingConfig: config.TracingSpec{
			Zipkin: config.ZipkinSpec{
				EndpointAddress: "http://foo.bar",
			},
		},
		expectedExporters: []trace.Exporter{&zipkin.Exporter{}},
	}, {
		name: "stdout trace exporter",
		tracingConfig: config.TracingSpec{
			Stdout: true,
		},
		expectedExporters: []trace.Exporter{&diag_utils.StdoutExporter{}},
	}, {
		name: "all trace exporters",
		tracingConfig: config.TracingSpec{
			Zipkin: config.ZipkinSpec{
				EndpointAddress: "http://foo.bar",
			},
			Stdout: true,
		},
		expectedExporters: []trace.Exporter{&diag_utils.StdoutExporter{}, &zipkin.Exporter{}},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rt := NewTestDaprRuntime(modes.StandaloneMode)
			rt.globalConfig.Spec.TracingSpec = tc.tracingConfig
			if tc.hostAddress != "" {
				rt.hostAddress = tc.hostAddress
			}
			// Setup tracing with the fake trace exporter store to confirm
			// the right exporter was registered.
			exporterStore := &fakeTraceExporterStore{}
			if err := rt.setupTracing(rt.hostAddress, exporterStore); tc.expectedErr != "" {
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.Nil(t, err)
			}
			for i, exporter := range exporterStore.exporters {
				// Exporter types don't expose internals, so we can only validate that
				// the right type of  exporter was registered.
				assert.Equal(t, reflect.TypeOf(tc.expectedExporters[i]), reflect.TypeOf(exporter))
			}
			// Setup tracing with the OpenCensus global exporter store.
			// We have no way to validate the result, but we can at least
			// confirm that nothing blows up.
			rt.setupTracing(rt.hostAddress, openCensusExporterStore{})
		})
	}
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	pubsubComponents := []components_v1alpha1.Component{
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Metadata: getFakeMetadataItems(),
			},
		}, {
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestSecondPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub2",
				Metadata: getFakeMetadataItems(),
			},
		},
	}

	initMockPubSubForRuntime := func(rt *DaprRuntime) (*daprt.MockPubSub, *daprt.MockPubSub) {
		mockPubSub := new(daprt.MockPubSub)

		mockPubSub2 := new(daprt.MockPubSub)

		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return mockPubSub
			}),

			pubsub_loader.New("mockPubSub2", func() pubsub.PubSub {
				return mockPubSub2
			}),
		)

		expectedMetadata := pubsub.Metadata{
			Properties: getFakeProperties(),
		}

		mockPubSub.On("Init", expectedMetadata).Return(nil)
		mockPubSub.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("func(*pubsub.NewMessage) error")).Return(nil)

		mockPubSub2.On("Init", expectedMetadata).Return(nil)
		mockPubSub2.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("func(*pubsub.NewMessage) error")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		rt.topicRoutes = nil
		rt.pubSubs = make(map[string]pubsub.PubSub)

		return mockPubSub, mockPubSub2
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"})           // second pubsub
		fakeResp.WithRawData([]byte(subs), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe to topic with custom route", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes to a topic via http app channel
		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		sub := getSubscriptionCustom("topic0", "customroute/topic0")
		fakeResp.WithRawData([]byte(sub), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")
		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("publish adapter is nil, no pub sub component", func(t *testing.T) {
		rt = NewTestDaprRuntime(modes.StandaloneMode)
		a := rt.getPublishAdapter()
		assert.Nil(t, a)
	})

	t.Run("publish adapter not nil, with pub sub component", func(t *testing.T) {
		rt = NewTestDaprRuntime(modes.StandaloneMode)
		rt.pubSubs[TestPubsubName], _ = initMockPubSubForRuntime(rt)
		a := rt.getPublishAdapter()
		assert.NotNil(t, a)
	})

	t.Run("load declarative subscription, no scopes", func(t *testing.T) {
		dir := "./components"

		rt = NewTestDaprRuntime(modes.StandaloneMode)

		os.Mkdir(dir, 0777)
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rt.runtimeConfig.Standalone.ComponentsPath = dir
		subs := rt.getDeclarativeSubscriptions()
		assert.Len(t, subs, 1)
		assert.Equal(t, "topic1", subs[0].Topic)
		assert.Equal(t, "myroute", subs[0].Route)
		assert.Equal(t, "pubsub", subs[0].PubsubName)
	})

	t.Run("load declarative subscription, in scopes", func(t *testing.T) {
		dir := "./components"

		rt = NewTestDaprRuntime(modes.StandaloneMode)

		os.Mkdir(dir, 0777)
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{TestRuntimeConfigID}

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rt.runtimeConfig.Standalone.ComponentsPath = dir
		subs := rt.getDeclarativeSubscriptions()
		assert.Len(t, subs, 1)
		assert.Equal(t, "topic1", subs[0].Topic)
		assert.Equal(t, "myroute", subs[0].Route)
		assert.Equal(t, "pubsub", subs[0].PubsubName)
		assert.Equal(t, TestRuntimeConfigID, subs[0].Scopes[0])
	})

	t.Run("load declarative subscription, not in scopes", func(t *testing.T) {
		dir := "./components"

		rt = NewTestDaprRuntime(modes.StandaloneMode)

		os.Mkdir(dir, 0777)
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{"scope1"}

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rt.runtimeConfig.Standalone.ComponentsPath = dir
		subs := rt.getDeclarativeSubscriptions()
		assert.Len(t, subs, 0)
	})

	t.Run("test subscribe, app allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app allowed 2 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 2 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0", "topic1"}, []string{"topic0"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app not allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic3"}, []string{"topic5"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)

		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe, app not allowed 1 topic, allowed one topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic0", "topic3"}, []string{"topic0", "topic5"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.startSubscribing()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
		})

		assert.Nil(t, err)

		rt.pubSubs[TestSecondPubsubName] = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		assert.Nil(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic2", rt.scopedPublishings[TestPubsubName])
		assert.False(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"topic2"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"A", "B"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"A"}}

		a := rt.isPubSubOperationAllowed(TestPubsubName, "A", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)

		b := rt.isPubSubOperationAllowed(TestPubsubName, "B", rt.scopedPublishings[TestPubsubName])
		assert.False(t, b)
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		err := rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})
		assert.Nil(t, err)
	})

	t.Run("secret store is registered", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})
		assert.NotNil(t, rt.secretStores["kubernetesMock"])
	})

	t.Run("get secret store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}),
		)

		rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})

		s := rt.getSecretStore("kubernetesMock")
		assert.NotNil(t, s)
	})
}

func TestMetadataItemsToPropertiesConversion(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		items := []components_v1alpha1.MetadataItem{
			{
				Name: "a",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte("b")},
				},
			},
		}
		m := rt.convertMetadataItemsToProperties(items)
		assert.Equal(t, 1, len(m))
		assert.Equal(t, "b", m["a"])
	})

	t.Run("int", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		items := []components_v1alpha1.MetadataItem{
			{
				Name: "a",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte(strconv.Itoa(6))},
				},
			},
		}
		m := rt.convertMetadataItemsToProperties(items)
		assert.Equal(t, 1, len(m))
		assert.Equal(t, "6", m["a"])
	})

	t.Run("bool", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		items := []components_v1alpha1.MetadataItem{
			{
				Name: "a",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte("true")},
				},
			},
		}
		m := rt.convertMetadataItemsToProperties(items)
		assert.Equal(t, 1, len(m))
		assert.Equal(t, "true", m["a"])
	})

	t.Run("float", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		items := []components_v1alpha1.MetadataItem{
			{
				Name: "a",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte("5.5")},
				},
			},
		}
		m := rt.convertMetadataItemsToProperties(items)
		assert.Equal(t, 1, len(m))
		assert.Equal(t, "5.5", m["a"])
	})

	t.Run("JSON string", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		items := []components_v1alpha1.MetadataItem{
			{
				Name: "a",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte(`"hello there"`)},
				},
			},
		}
		m := rt.convertMetadataItemsToProperties(items)
		assert.Equal(t, 1, len(m))
		assert.Equal(t, "hello there", m["a"])
	})
}

func TestPopulateSecretsConfiguration(t *testing.T) {
	t.Run("secret store configuration is populated", func(t *testing.T) {
		// setup
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.globalConfig.Spec.Secrets.Scopes = []config.SecretsScope{
			{
				StoreName:     "testMock",
				DefaultAccess: "allow",
			},
		}

		// act
		rt.populateSecretsConfiguration()

		// verify
		assert.Contains(t, rt.secretsConfiguration, "testMock", "Expected testMock secret store configuration to be populated")
		assert.Equal(t, config.AllowAccess, rt.secretsConfiguration["testMock"].DefaultAccess, "Expected default access as allow")
		assert.Empty(t, rt.secretsConfiguration["testMock"].DeniedSecrets, "Expected testMock deniedSecrets to not be populated")
		assert.NotContains(t, rt.secretsConfiguration["testMock"].AllowedSecrets, "Expected testMock allowedSecrets to not be populated")
	})
}

func TestProcessComponentSecrets(t *testing.T) {
	mockBinding := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "mockBinding",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type: "bindings.mock",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				{
					Name: "b",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{Raw: []byte("value2")},
					},
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "kubernetes",
		},
	}

	t.Run("Standalone Mode", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = components_v1alpha1.DynamicValue{
			JSON: v1.JSON{Raw: []byte("")},
		}
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Key:  "key1",
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// add Kubernetes component manually
		rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetes",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetes",
			},
		})

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Kubernetes Mode", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = components_v1alpha1.DynamicValue{
			JSON: v1.JSON{Raw: []byte("")},
		}
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Key:  "key1",
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		for _, comp := range rt.builtinSecretStore() {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Look up name only", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = components_v1alpha1.DynamicValue{
			JSON: v1.JSON{Raw: []byte("")},
		}
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		for _, comp := range rt.builtinSecretStore() {
			err := rt.processComponentAndDependents(comp)
			assert.Nil(t, err)
		}

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})
}

func TestExtractComponentCategory(t *testing.T) {
	compCategoryTests := []struct {
		specType string
		category string
	}{
		{"pubsub.redis", "pubsub"},
		{"pubsubs.redis", ""},
		{"secretstores.azure.keyvault", "secretstores"},
		{"secretstore.azure.keyvault", ""},
		{"exporters.zipkin", "exporters"},
		{"exporter.zipkin", ""},
		{"state.redis", "state"},
		{"states.redis", ""},
		{"bindings.kafka", "bindings"},
		{"binding.kafka", ""},
		{"this.is.invalid.category", ""},
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)

	for _, tt := range compCategoryTests {
		t.Run(tt.specType, func(t *testing.T) {
			fakeComp := components_v1alpha1.Component{
				Spec: components_v1alpha1.ComponentSpec{
					Type: tt.specType,
				},
			}
			assert.Equal(t, string(rt.extractComponentCategory(fakeComp)), tt.category)
		})
	}
}

// Test that flushOutstandingComponents waits for components
func TestFlushOutstandingComponent(t *testing.T) {
	t.Run("We can call flushOustandingComponents more than once", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		wasCalled := false
		m := NewMockKubernetesStoreWithInitCallback(func() {
			time.Sleep(100 * time.Millisecond)
			wasCalled = true
		})
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		go rt.processComponents()
		rt.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)

		// Make sure that the goroutine was restarted and can flush a second time
		wasCalled = false
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock2", func() secretstores.SecretStore {
				return m
			}))

		rt.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock2",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)
	})
	t.Run("flushOutstandingComponents blocks for components with outstanding dependanices", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		wasCalled := false
		wasCalledChild := false
		wasCalledGrandChild := false
		m := NewMockKubernetesStoreWithInitCallback(func() {
			time.Sleep(100 * time.Millisecond)
			wasCalled = true
		})
		mc := NewMockKubernetesStoreWithInitCallback(func() {
			time.Sleep(100 * time.Millisecond)
			wasCalledChild = true
		})
		mgc := NewMockKubernetesStoreWithInitCallback(func() {
			time.Sleep(100 * time.Millisecond)
			wasCalledGrandChild = true
		})
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMockChild", func() secretstores.SecretStore {
				return mc
			}))
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMockGrandChild", func() secretstores.SecretStore {
				return mgc
			}))

		go rt.processComponents()
		rt.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMockGrandChild",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMockGrandChild",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "a",
						SecretKeyRef: components_v1alpha1.SecretKeyRef{
							Key:  "key1",
							Name: "name1",
						},
					},
				},
			},
			Auth: components_v1alpha1.Auth{
				SecretStore: "kubernetesMockChild",
			},
		}
		rt.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMockChild",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMockChild",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "a",
						SecretKeyRef: components_v1alpha1.SecretKeyRef{
							Key:  "key1",
							Name: "name1",
						},
					},
				},
			},
			Auth: components_v1alpha1.Auth{
				SecretStore: "kubernetesMock",
			},
		}
		rt.pendingComponents <- components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)
		assert.True(t, wasCalledChild)
		assert.True(t, wasCalledGrandChild)
	})
}

// Test InitSecretStore if secretstore.* refers to Kubernetes secret store
func TestInitSecretStoresInKubernetesMode(t *testing.T) {
	fakeSecretStoreWithAuth := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "fakeSecretStore",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type: "secretstores.fake.secretstore",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				{
					Name: "b",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{Raw: []byte("value2")},
					},
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "kubernetes",
		},
	}

	rt := NewTestDaprRuntime(modes.KubernetesMode)

	m := NewMockKubernetesStore()
	rt.secretStoresRegistry.Register(
		secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
			return m
		}),
	)
	for _, comp := range rt.builtinSecretStore() {
		err := rt.processComponentAndDependents(comp)
		assert.Nil(t, err)
	}
	fakeSecretStoreWithAuth, _ = rt.processComponentSecrets(fakeSecretStoreWithAuth)
	// initSecretStore appends Kubernetes component even if kubernetes component is not added
	assert.Equal(t, "value1", string(fakeSecretStoreWithAuth.Spec.Metadata[0].Value.Raw))
}

func TestOnNewPublishedMessage(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic, TestSecondPubsubName, "", []byte("Test Message"))
	b, err := json.Marshal(envelope)
	assert.Nil(t, err)

	testPubSubMessage := &pubsub.NewMessage{
		Topic:    topic,
		Data:     b,
		Metadata: map[string]string{pubsubName: TestPubsubName},
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.Topic)
	fakeReq.WithHTTPExtension(http.MethodPost, "")
	fakeReq.WithRawData(testPubSubMessage.Data, pubsub.ContentType)

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.topicRoutes = map[string]TopicRoute{}
	rt.topicRoutes[TestPubsubName] = TopicRoute{routes: make(map[string]Route)}
	rt.topicRoutes[TestPubsubName].routes["topic1"] = Route{path: "topic1"}

	t.Run("succeeded to publish message to user app with empty response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with non-json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with status", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"SUCCESS\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask for retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"RETRY\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		var cloudEvent pubsub.CloudEventsEnvelope
		json := jsoniter.ConfigFastest
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent.ID)
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask to drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"DROP\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned unknown status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"not_valid\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Error(t, err, "expected error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned empty status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"message\": \"empty status\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.NoError(t, err, "expected no error on empty status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app and app returned unexpected json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"message\": \"success\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err, "expected no error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message error on invoking method", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		invokeError := errors.New("error invoking method")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(nil, invokeError)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		expectedError := errors.Wrap(invokeError, "error from app channel while sending pub/sub event to app")
		assert.Equal(t, expectedError.Error(), err.Error(), "expected errors to match")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 404", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		clientError := errors.New("Not Found")
		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)
		fakeResp.WithRawData([]byte(clientError.Error()), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err, "expected error to be nil")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 500", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		clientError := errors.New("Internal Error")
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil)
		fakeResp.WithRawData([]byte(clientError.Error()), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		var cloudEvent pubsub.CloudEventsEnvelope
		json := jsoniter.ConfigFastest
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := errors.Errorf("retriable error returned from app while processing pub/sub event %v: Internal Error. status code returned: 500", cloudEvent.ID)
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestOnNewPublishedMessageGRPC(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic, TestSecondPubsubName, "", []byte("Test Message"))
	b, err := json.Marshal(envelope)
	assert.Nil(t, err)

	testPubSubMessage := &pubsub.NewMessage{
		Topic:    topic,
		Data:     b,
		Metadata: map[string]string{pubsubName: TestPubsubName},
	}

	testCases := []struct {
		name             string
		responseStatus   runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		errorExpected    bool
		noResponseStatus bool
		responseError    error
	}{
		{
			name:             "failed to publish message to user app with unimplemented error",
			noResponseStatus: true,
			responseError:    status.Errorf(codes.Unimplemented, "unimplemented method"),
			errorExpected:    false, // should be dropped with no error
		},
		{
			name:             "failed to publish message to user app with response error",
			noResponseStatus: true,
			responseError:    assert.AnError,
			errorExpected:    true,
		},
		{
			name:             "succeeded to publish message to user app with empty response",
			noResponseStatus: true,
		},
		{
			name:           "succeeded to publish message to user app with success response",
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			name:           "succeeded to publish message to user app with retry",
			responseStatus: runtimev1pb.TopicEventResponse_RETRY,
			errorExpected:  true,
		},
		{
			name:           "succeeded to publish message to user app with drop",
			responseStatus: runtimev1pb.TopicEventResponse_DROP,
		},
		{
			name:           "succeeded to publish message to user app with invalid response",
			responseStatus: runtimev1pb.TopicEventResponse_TopicEventResponseStatus(99),
			errorExpected:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// setup
			// getting new port for every run to avoid conflict and timing issues between tests if sharing same port
			port, _ := freeport.GetFreePort()
			rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, string(GRPCProtocol), port)
			rt.topicRoutes = map[string]TopicRoute{}
			rt.topicRoutes[TestPubsubName] = TopicRoute{
				routes: map[string]Route{
					topic: {path: topic},
				},
			}
			var grpcServer *grpc.Server

			// create mock application server first
			if !tc.noResponseStatus {
				grpcServer = startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					TopicEventResponseStatus: tc.responseStatus,
					Error:                    tc.responseError,
				})
			} else {
				grpcServer = startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					Error: tc.responseError,
				})
			}
			if grpcServer != nil {
				// properly stop the gRPC server
				defer grpcServer.Stop()
			}

			// create a new AppChannel and gRPC client for every test
			rt.createAppChannel()
			// properly close the app channel created
			defer rt.grpc.AppClient.Close()

			// act
			err = rt.publishMessageGRPC(testPubSubMessage)

			// assert
			if tc.errorExpected {
				assert.Error(t, err, "expected an error")
			} else {
				assert.Nil(t, err, "expected no error")
			}
		})
	}
}

func TestGetSubscribedBindingsGRPC(t *testing.T) {
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
			rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, string(GRPCProtocol), port)
			// create mock application server first
			grpcServer := startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
				Error:    tc.responseError,
				Bindings: tc.responseFromApp,
			})
			defer grpcServer.Stop()

			// create a new AppChannel and gRPC client for every test
			rt.createAppChannel()
			// properly close the app channel created
			defer rt.grpc.AppClient.Close()

			// act
			resp := rt.getSubscribedBindingsGRPC()

			// assert
			assert.Equal(t, tc.expectedResponse, resp, "expected response to match")
		})
	}
}

func startTestAppCallbackGRPCServer(t *testing.T, port int, mockServer runtimev1pb.AppCallbackServer) *grpc.Server {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)
	grpcServer := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, mockServer)
		if err := grpcServer.Serve(lis); err != nil {
			panic(err)
		}
	}()
	// wait until server starts
	time.Sleep(maxGRPCServerUptime)

	return grpcServer
}

func getFakeProperties() map[string]string {
	return map[string]string{
		"host":                    "localhost",
		"password":                "fakePassword",
		"consumerID":              TestRuntimeConfigID,
		scopes.SubscriptionScopes: fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
		scopes.PublishingScopes:   fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
	}
}

func getFakeMetadataItems() []components_v1alpha1.MetadataItem {
	return []components_v1alpha1.MetadataItem{
		{
			Name: "host",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("localhost"),
				},
			},
		},
		{
			Name: "password",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("fakePassword"),
				},
			},
		},
		{
			Name: "consumerID",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(TestRuntimeConfigID),
				},
			},
		},
		{
			Name: scopes.SubscriptionScopes,
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID)),
				},
			},
		},
		{
			Name: scopes.PublishingScopes,
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID)),
				},
			},
		},
	}
}

func NewTestDaprRuntime(mode modes.DaprMode) *DaprRuntime {
	return NewTestDaprRuntimeWithProtocol(mode, string(HTTPProtocol), 1024)
}

func NewTestDaprRuntimeWithProtocol(mode modes.DaprMode, protocol string, appPort int) *DaprRuntime {
	testRuntimeConfig := NewRuntimeConfig(
		TestRuntimeConfigID,
		[]string{"10.10.10.12"},
		"10.10.10.11",
		cors.DefaultAllowedOrigins,
		"globalConfig",
		"",
		protocol,
		string(mode),
		DefaultDaprHTTPPort,
		0,
		DefaultDaprAPIGRPCPort,
		appPort,
		DefaultProfilePort,
		false,
		-1,
		false,
		"",
		false)

	rt := NewDaprRuntime(testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{})
	return rt
}

func TestMTLS(t *testing.T) {
	t.Run("with mTLS enabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.runtimeConfig.mtlsEnabled = true
		rt.runtimeConfig.SentryServiceAddress = "1.1.1.1"

		os.Setenv(certs.TrustAnchorsEnvVar, testCertRoot)
		os.Setenv(certs.CertChainEnvVar, "a")
		os.Setenv(certs.CertKeyEnvVar, "b")
		defer os.Clearenv()

		certChain, err := security.GetCertChain()
		assert.Nil(t, err)
		rt.runtimeConfig.CertChain = certChain

		err = rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
		assert.NotNil(t, rt.authenticator)
	})

	t.Run("with mTLS disabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		err := rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
		assert.Nil(t, rt.authenticator)
	})
}

type mockBinding struct {
	hasError bool
	data     string
	metadata map[string]string
}

func (b *mockBinding) Init(metadata bindings.Metadata) error {
	return nil
}

func (b *mockBinding) Read(handler func(*bindings.ReadResponse) error) error {
	b.data = string(testInputBindingData)
	metadata := map[string]string{}
	if b.metadata != nil {
		metadata = b.metadata
	}

	err := handler(&bindings.ReadResponse{
		Metadata: metadata,
		Data:     []byte(b.data),
	})
	b.hasError = err != nil
	return nil
}

func (b *mockBinding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{"create"}
}

func (b *mockBinding) Invoke(req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	return nil, nil
}

func TestInvokeOutputBindings(t *testing.T) {
	t.Run("output binding missing operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data: []byte(""),
		})
		assert.NotNil(t, err)
		assert.Equal(t, "operation field is missing from request", err.Error())
	})

	t.Run("output binding valid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.outputBindings["mockBinding"] = &mockBinding{}

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.CreateOperation,
		})
		assert.Nil(t, err)
	})

	t.Run("output binding invalid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.outputBindings["mockBinding"] = &mockBinding{}

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.GetOperation,
		})
		assert.NotNil(t, err)
		assert.Equal(t, "binding mockBinding does not support operation get. supported operations: create", err.Error())
	})
}

func TestReadInputBindings(t *testing.T) {
	const testInputBindingName = "inputbinding"
	const testInputBindingMethod = "inputbinding"

	t.Run("app acknowledge, no retry", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeBindingReq.WithHTTPExtension(http.MethodOptions, "")
		fakeBindingReq.WithRawData(nil, invokev1.JSONContentType)

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData(testInputBindingData, "application/json")
		fakeReq.WithMetadata(map[string][]string{})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeBindingReq).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding(testInputBindingName, &b)

		assert.False(t, b.hasError)
	})

	t.Run("app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeBindingReq.WithHTTPExtension(http.MethodOptions, "")
		fakeBindingReq.WithRawData(nil, invokev1.JSONContentType)

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData(testInputBindingData, "application/json")
		fakeReq.WithMetadata(map[string][]string{})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil)
		fakeResp.WithRawData([]byte("Internal Error"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeBindingReq).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding(testInputBindingName, &b)

		assert.True(t, b.hasError)
	})

	t.Run("binding has data and metadata", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeBindingReq.WithHTTPExtension(http.MethodOptions, "")
		fakeBindingReq.WithRawData(nil, invokev1.JSONContentType)

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod)
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData(testInputBindingData, "application/json")
		fakeReq.WithMetadata(map[string][]string{"bindings": {"input"}})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeBindingReq).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		b := mockBinding{metadata: map[string]string{"bindings": "input"}}
		rt.readFromBinding(testInputBindingName, &b)

		assert.Equal(t, string(testInputBindingData), b.data)
	})
}

func TestNamespace(t *testing.T) {
	t.Run("empty namespace", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		ns := rt.getNamespace()

		assert.Empty(t, ns)
	})

	t.Run("non-empty namespace", func(t *testing.T) {
		os.Setenv("NAMESPACE", "a")
		defer os.Clearenv()

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		ns := rt.getNamespace()

		assert.Equal(t, "a", ns)
	})
}

func TestAuthorizedComponents(t *testing.T) {
	testCompName := "fakeComponent"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
		assert.Equal(t, testCompName, comps[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{TestRuntimeConfigID}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{"other"}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{TestRuntimeConfigID}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})
}

type mockPublishPubSub struct {
}

// Init is a mock initialization method
func (m *mockPublishPubSub) Init(metadata pubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method
func (m *mockPublishPubSub) Publish(req *pubsub.PublishRequest) error {
	return nil
}

// Subscribe is a mock subscribe method
func (m *mockPublishPubSub) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	return nil
}

func (m *mockPublishPubSub) Close() error {
	return nil
}

func TestInitActors(t *testing.T) {
	t.Run("missing namespace on kubernetes", func(t *testing.T) {
		r := NewDaprRuntime(&Config{Mode: modes.KubernetesMode}, &config.Configuration{}, &config.AccessControlList{})
		r.namespace = ""
		r.runtimeConfig.mtlsEnabled = true

		err := r.initActors()
		assert.Error(t, err)
	})
}

func TestInitBindings(t *testing.T) {
	t.Run("single input binding", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{})
		r.bindingsRegistry.RegisterInputBindings(
			bindings_loader.NewInput("testInputBinding", func() bindings.InputBinding {
				return &daprt.MockBinding{}
			}),
		)

		c := components_v1alpha1.Component{}
		c.ObjectMeta.Name = "testInputBinding"
		c.Spec.Type = "bindings.testInputBinding"
		err := r.initBinding(c)
		assert.NoError(t, err)
	})

	t.Run("single output binding", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{})
		r.bindingsRegistry.RegisterOutputBindings(
			bindings_loader.NewOutput("testOutputBinding", func() bindings.OutputBinding {
				return &daprt.MockBinding{}
			}),
		)

		c := components_v1alpha1.Component{}
		c.ObjectMeta.Name = "testOutputBinding"
		c.Spec.Type = "bindings.testOutputBinding"
		err := r.initBinding(c)
		assert.NoError(t, err)
	})

	t.Run("one input binding, one output binding", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{})
		r.bindingsRegistry.RegisterInputBindings(
			bindings_loader.NewInput("testinput", func() bindings.InputBinding {
				return &daprt.MockBinding{}
			}),
		)

		r.bindingsRegistry.RegisterOutputBindings(
			bindings_loader.NewOutput("testoutput", func() bindings.OutputBinding {
				return &daprt.MockBinding{}
			}),
		)

		input := components_v1alpha1.Component{}
		input.ObjectMeta.Name = "testinput"
		input.Spec.Type = "bindings.testinput"
		err := r.initBinding(input)
		assert.NoError(t, err)

		output := components_v1alpha1.Component{}
		output.ObjectMeta.Name = "testinput"
		output.Spec.Type = "bindings.testoutput"
		err = r.initBinding(output)
		assert.NoError(t, err)
	})
}
