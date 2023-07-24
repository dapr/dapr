/*
Copyright 2021 The Dapr Authors
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

//nolint:nosnakecase
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/zipkin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/components-contrib/lock"
	mdata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/middleware"
	"github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config/protocol"

	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	modeconfig "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/cors"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/expr"
	pb "github.com/dapr/dapr/pkg/grpc/proxy/testservice"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	"github.com/dapr/dapr/pkg/scopes"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	TestRuntimeConfigID  = "consumer0"
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	TestLockName         = "testlock"
	resourcesDir         = "./components"
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

var testResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  ptr.Of(1),
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
			},
			Timeouts: map[string]string{
				"fast": "100ms",
			},
		},
		Targets: v1alpha1.Targets{
			Components: map[string]v1alpha1.ComponentPolicyNames{
				"failOutput": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
				"failPubsub": {
					Outbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
					Inbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
				"failingInputBinding": {
					Inbound: v1alpha1.PolicyNames{
						Retry:   "singleRetry",
						Timeout: "fast",
					},
				},
			},
		},
	},
}

func TestNewRuntime(t *testing.T) {
	// act
	r := newDaprRuntime(&internalConfig{
		registry: registry.New(registry.NewOptions()),
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

// writeComponentToDisk the given content into a file inside components directory.
func writeComponentToDisk(content any, fileName string) (cleanup func(), error error) {
	filePath := fmt.Sprintf("%s/%s", resourcesDir, fileName)
	b, err := yaml.Marshal(content)
	if err != nil {
		return nil, err
	}
	return func() {
		os.Remove(filePath)
	}, os.WriteFile(filePath, b, 0o600)
}

func getSubscriptionCustom(topic, path string) string {
	s := []runtimePubsub.SubscriptionJSON{
		{
			PubsubName: TestPubsubName,
			Topic:      topic,
			Routes: runtimePubsub.RoutesJSON{
				Default: path,
			},
		},
	}
	b, _ := json.Marshal(&s)
	return string(b)
}

func testDeclarativeSubscription() subscriptionsapi.Subscription {
	return subscriptionsapi.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: "v1alpha1",
		},
		Spec: subscriptionsapi.SubscriptionSpec{
			Topic:      "topic1",
			Route:      "myroute",
			Pubsubname: "pubsub",
		},
	}
}

func TestProcessComponentsAndDependents(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	incorrectComponentType := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsubs.mockPubSub",
			Version:  "v1",
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
	defer stopRuntime(t, rt)

	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	lockComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestLockName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "lock.mockLock",
			Version: "v1",
		},
	}

	t.Run("test error on lock init", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(context.Background(), gomock.Any()).Return(assert.AnError)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		// act
		err := rt.processor.One(context.TODO(), lockComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "testlock (lock.mockLock/v1)", assert.AnError).Error(), "expected error strings to match")
	})

	t.Run("test error when lock version invalid", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		lockComponentV3 := lockComponent
		lockComponentV3.Spec.Version = "v3"

		// act
		err := rt.processor.One(context.TODO(), lockComponentV3)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.CreateComponentFailure, "testlock (lock.mockLock/v3)", fmt.Errorf("couldn't find lock store lock.mockLock/v3")).Error())
	})

	t.Run("test error when lock prefix strategy invalid", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(context.Background(), gomock.Any()).Return(nil)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		lockComponentWithWrongStrategy := lockComponent
		lockComponentWithWrongStrategy.Spec.Metadata = []commonapi.NameValuePair{
			{
				Name: "keyPrefix",
				Value: commonapi.DynamicValue{
					JSON: v1.JSON{Raw: []byte("||")},
				},
			},
		}
		// act
		err := rt.processor.One(context.TODO(), lockComponentWithWrongStrategy)
		// assert
		assert.Error(t, err)
	})

	t.Run("lock init successfully and set right strategy", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(context.Background(), gomock.Any()).Return(nil)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		// act
		err := rt.processor.One(context.TODO(), lockComponent)
		// assert
		assert.Nil(t, err, "unexpected error")
		// get modified key
		key, err := lockLoader.GetModifiedLockKey("test", "mockLock", "appid-1")
		assert.Nil(t, err, "unexpected error")
		assert.Equal(t, key, "lock||appid-1||test")
	})

	t.Run("test error on pubsub init", func(t *testing.T) {
		// setup
		mockPubSub := new(daprt.MockPubSub)

		rt.runtimeConfig.registry.PubSubs().RegisterComponent(
			func(_ logger.Logger) pubsub.PubSub {
				return mockPubSub
			},
			"mockPubSub",
		)
		expectedMetadata := pubsub.Metadata{
			Base: mdata.Base{
				Name:       TestPubsubName,
				Properties: getFakeProperties(),
			},
		}

		mockPubSub.On("Init", expectedMetadata).Return(assert.AnError)

		// act
		err := rt.processor.One(context.TODO(), pubsubComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "testpubsub (pubsub.mockPubSub/v1)", assert.AnError).Error(), "expected error strings to match")
	})

	t.Run("test invalid category component", func(t *testing.T) {
		// act
		err := rt.processor.One(context.TODO(), componentsV1alpha1.Component{
			Spec: componentsV1alpha1.ComponentSpec{
				Type: "invalid",
			},
		})
		// assert
		assert.Error(t, err, "error expected")
	})
}

// mockOperatorClient is a mock implementation of operatorv1pb.OperatorClient.
// It is used to test `beginComponentsUpdates` and `beginHTTPEndpointsUpdates`.
type mockOperatorClient struct {
	operatorv1pb.OperatorClient

	lock                              sync.RWMutex
	compsByName                       map[string]*componentsV1alpha1.Component
	endpointsByName                   map[string]*httpEndpointV1alpha1.HTTPEndpoint
	clientStreams                     []*mockOperatorComponentUpdateClientStream
	clientEndpointStreams             []*mockOperatorHTTPEndpointUpdateClientStream
	clientStreamCreateWait            chan struct{}
	clientStreamCreatedNotify         chan struct{}
	clientEndpointStreamCreateWait    chan struct{}
	clientEndpointStreamCreatedNotify chan struct{}
}

func newMockOperatorClient() *mockOperatorClient {
	mockOpCli := &mockOperatorClient{
		compsByName:                       make(map[string]*componentsV1alpha1.Component),
		endpointsByName:                   make(map[string]*httpEndpointV1alpha1.HTTPEndpoint),
		clientStreams:                     make([]*mockOperatorComponentUpdateClientStream, 0, 1),
		clientEndpointStreams:             make([]*mockOperatorHTTPEndpointUpdateClientStream, 0, 1),
		clientStreamCreateWait:            make(chan struct{}, 1),
		clientStreamCreatedNotify:         make(chan struct{}, 1),
		clientEndpointStreamCreateWait:    make(chan struct{}, 1),
		clientEndpointStreamCreatedNotify: make(chan struct{}, 1),
	}
	return mockOpCli
}

func (c *mockOperatorClient) ComponentUpdate(ctx context.Context, in *operatorv1pb.ComponentUpdateRequest, opts ...grpc.CallOption) (operatorv1pb.Operator_ComponentUpdateClient, error) {
	// Used to block stream creation.
	<-c.clientStreamCreateWait

	cs := &mockOperatorComponentUpdateClientStream{
		updateCh: make(chan *operatorv1pb.ComponentUpdateEvent, 1),
	}

	c.lock.Lock()
	c.clientStreams = append(c.clientStreams, cs)
	c.lock.Unlock()

	c.clientStreamCreatedNotify <- struct{}{}

	return cs, nil
}

func (c *mockOperatorClient) HTTPEndpointUpdate(ctx context.Context, in *operatorv1pb.HTTPEndpointUpdateRequest, opts ...grpc.CallOption) (operatorv1pb.Operator_HTTPEndpointUpdateClient, error) {
	// Used to block stream creation.
	<-c.clientEndpointStreamCreateWait

	cs := &mockOperatorHTTPEndpointUpdateClientStream{
		updateCh: make(chan *operatorv1pb.HTTPEndpointUpdateEvent, 1),
	}

	c.lock.Lock()
	c.clientEndpointStreams = append(c.clientEndpointStreams, cs)
	c.lock.Unlock()

	c.clientEndpointStreamCreatedNotify <- struct{}{}

	return cs, nil
}

func (c *mockOperatorClient) ListComponents(ctx context.Context, in *operatorv1pb.ListComponentsRequest, opts ...grpc.CallOption) (*operatorv1pb.ListComponentResponse, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	resp := &operatorv1pb.ListComponentResponse{
		Components: [][]byte{},
	}
	for _, comp := range c.compsByName {
		b, err := json.Marshal(comp)
		if err != nil {
			continue
		}
		resp.Components = append(resp.Components, b)
	}
	return resp, nil
}

func (c *mockOperatorClient) ListHTTPEndpoints(ctx context.Context, in *operatorv1pb.ListHTTPEndpointsRequest, opts ...grpc.CallOption) (*operatorv1pb.ListHTTPEndpointsResponse, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	resp := &operatorv1pb.ListHTTPEndpointsResponse{
		HttpEndpoints: [][]byte{},
	}
	for _, end := range c.endpointsByName {
		b, err := json.Marshal(end)
		if err != nil {
			continue
		}
		resp.HttpEndpoints = append(resp.HttpEndpoints, b)
	}
	return resp, nil
}

func (c *mockOperatorClient) ClientStreamCount() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.clientStreams)
}

func (c *mockOperatorClient) ClientHTTPEndpointStreamCount() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.clientEndpointStreams)
}

func (c *mockOperatorClient) AllowOneNewClientStreamCreate() {
	c.clientStreamCreateWait <- struct{}{}
}

func (c *mockOperatorClient) AllowOneNewClientEndpointStreamCreate() {
	c.clientEndpointStreamCreateWait <- struct{}{}
}

func (c *mockOperatorClient) WaitOneNewClientStreamCreated(ctx context.Context) error {
	select {
	case <-c.clientStreamCreatedNotify:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *mockOperatorClient) WaitOneNewClientHTTPEndpointStreamCreated(ctx context.Context) error {
	select {
	case <-c.clientEndpointStreamCreatedNotify:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *mockOperatorClient) CloseAllClientStreams() {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, cs := range c.clientStreams {
		close(cs.updateCh)
	}
	c.clientStreams = []*mockOperatorComponentUpdateClientStream{}
}

func (c *mockOperatorClient) CloseAllClientHTTPEndpointStreams() {
	c.lock.Lock()
	defer c.lock.Unlock()

	for _, cs := range c.clientEndpointStreams {
		close(cs.updateCh)
	}
	c.clientEndpointStreams = []*mockOperatorHTTPEndpointUpdateClientStream{}
}

func (c *mockOperatorClient) UpdateComponent(comp *componentsV1alpha1.Component) {
	b, err := json.Marshal(comp)
	if err != nil {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	c.compsByName[comp.Name] = comp
	for _, cs := range c.clientStreams {
		cs.updateCh <- &operatorv1pb.ComponentUpdateEvent{Component: b}
	}
}

func (c *mockOperatorClient) UpdateHTTPEndpoint(endpoint *httpEndpointV1alpha1.HTTPEndpoint) {
	b, err := json.Marshal(endpoint)
	if err != nil {
		return
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	c.endpointsByName[endpoint.Name] = endpoint
	for _, cs := range c.clientEndpointStreams {
		cs.updateCh <- &operatorv1pb.HTTPEndpointUpdateEvent{HttpEndpoints: b}
	}
}

type mockOperatorComponentUpdateClientStream struct {
	operatorv1pb.Operator_ComponentUpdateClient

	updateCh chan *operatorv1pb.ComponentUpdateEvent
}

type mockOperatorHTTPEndpointUpdateClientStream struct {
	operatorv1pb.Operator_HTTPEndpointUpdateClient

	updateCh chan *operatorv1pb.HTTPEndpointUpdateEvent
}

func (cs *mockOperatorComponentUpdateClientStream) Recv() (*operatorv1pb.ComponentUpdateEvent, error) {
	e, ok := <-cs.updateCh
	if !ok {
		return nil, fmt.Errorf("stream closed")
	}
	return e, nil
}

func (cs *mockOperatorHTTPEndpointUpdateClientStream) Recv() (*operatorv1pb.HTTPEndpointUpdateEvent, error) {
	e, ok := <-cs.updateCh
	if !ok {
		return nil, fmt.Errorf("stream closed")
	}
	return e, nil
}

func TestComponentsUpdate(t *testing.T) {
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	defer stopRuntime(t, rt)

	mockOpCli := newMockOperatorClient()
	rt.operatorClient = mockOpCli

	processedCh := make(chan struct{}, 1)
	mockProcessComponents := func() {
		for comp := range rt.pendingComponents {
			if comp.Name == "" {
				continue
			}
			rt.compStore.AddComponent(comp)
			processedCh <- struct{}{}
		}
	}
	go mockProcessComponents()

	go rt.beginComponentsUpdates()

	comp1 := &componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub1",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "pubsub.mockPubSub1",
			Version: "v1",
		},
	}
	comp2 := &componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub2",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "pubsub.mockPubSub2",
			Version: "v1",
		},
	}
	comp3 := &componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub3",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "pubsub.mockPubSub3",
			Version: "v1",
		},
	}

	// Allow a new stream to create.
	mockOpCli.AllowOneNewClientStreamCreate()
	// Wait a new stream created.
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if err := mockOpCli.WaitOneNewClientStreamCreated(waitCtx); err != nil {
		t.Errorf("Wait new stream err: %s", err.Error())
		t.FailNow()
	}

	// Wait comp1 received and processed.
	mockOpCli.UpdateComponent(comp1)
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect component [comp1] processed.")
		t.FailNow()
	}
	_, exists := rt.compStore.GetComponent(comp1.Spec.Type, comp1.Name)
	assert.True(t, exists, fmt.Sprintf("expect component, type: %s, name: %s", comp1.Spec.Type, comp1.Name))

	// Close all client streams to trigger an stream error in `beginComponentsUpdates`
	mockOpCli.CloseAllClientStreams()

	// Update during stream error.
	mockOpCli.UpdateComponent(comp2)

	// Assert no client stream created.
	assert.Equal(t, mockOpCli.ClientStreamCount(), 0, "Expect 0 client stream")

	// Allow a new stream to create.
	mockOpCli.AllowOneNewClientStreamCreate()
	// Wait a new stream created.
	waitCtx, cancel = context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if err := mockOpCli.WaitOneNewClientStreamCreated(waitCtx); err != nil {
		t.Errorf("Wait new stream err: %s", err.Error())
		t.FailNow()
	}

	// Wait comp2 received and processed.
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect component [comp2] processed.")
		t.FailNow()
	}
	_, exists = rt.compStore.GetComponent(comp2.Spec.Type, comp2.Name)
	assert.True(t, exists, fmt.Sprintf("Expect component, type: %s, name: %s", comp2.Spec.Type, comp2.Name))

	mockOpCli.UpdateComponent(comp3)

	// Wait comp3 received and processed.
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect component [comp3] processed.")
		t.FailNow()
	}
	_, exists = rt.compStore.GetComponent(comp3.Spec.Type, comp3.Name)
	assert.True(t, exists, fmt.Sprintf("Expect component, type: %s, name: %s", comp3.Spec.Type, comp3.Name))
}

func TestInitNameResolution(t *testing.T) {
	initMockResolverForRuntime := func(rt *DaprRuntime, resolverName string, e error) *daprt.MockResolver {
		mockResolver := new(daprt.MockResolver)

		rt.runtimeConfig.registry.NameResolutions().RegisterComponent(
			func(_ logger.Logger) nameresolution.Resolver {
				return mockResolver
			},
			resolverName,
		)

		expectedMetadata := nameresolution.Metadata{Base: mdata.Base{
			Name: resolverName,
			Properties: map[string]string{
				nameresolution.DaprHTTPPort: strconv.Itoa(rt.runtimeConfig.httpPort),
				nameresolution.DaprPort:     strconv.Itoa(rt.runtimeConfig.internalGRPCPort),
				nameresolution.AppPort:      strconv.Itoa(rt.runtimeConfig.appConnectionConfig.Port),
				nameresolution.HostAddress:  rt.hostAddress,
				nameresolution.AppID:        rt.runtimeConfig.id,
			},
		}}

		mockResolver.On("Init", expectedMetadata).Return(e)

		return mockResolver
	}

	t.Run("error on unknown resolver", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "targetResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "anotherResolver", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.Error(t, err)
	})

	t.Run("test init nameresolution", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "someResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "someResolver", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in StandaloneMode", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in StandaloneMode", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in KubernetesMode", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.KubernetesMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in KubernetesMode", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.KubernetesMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err := rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})
}

func TestSetupTracing(t *testing.T) {
	testcases := []struct {
		name              string
		tracingConfig     config.TracingSpec
		hostAddress       string
		expectedExporters []sdktrace.SpanExporter
		expectedErr       string
	}{{
		name:          "no trace exporter",
		tracingConfig: config.TracingSpec{},
	}, {
		name: "sampling rate 1 without trace exporter",
		tracingConfig: config.TracingSpec{
			SamplingRate: "1",
		},
		expectedExporters: []sdktrace.SpanExporter{&diagUtils.NullExporter{}},
	}, {
		name: "bad host address, failing zipkin",
		tracingConfig: config.TracingSpec{
			Zipkin: &config.ZipkinSpec{
				EndpointAddress: "localhost",
			},
		},
		expectedErr: "invalid collector URL \"localhost\": no scheme or host",
	}, {
		name: "zipkin trace exporter",
		tracingConfig: config.TracingSpec{
			Zipkin: &config.ZipkinSpec{
				EndpointAddress: "http://foo.bar",
			},
		},
		expectedExporters: []sdktrace.SpanExporter{&zipkin.Exporter{}},
	}, {
		name: "otel trace http exporter",
		tracingConfig: config.TracingSpec{
			Otel: &config.OtelSpec{
				EndpointAddress: "foo.bar",
				IsSecure:        ptr.Of(false),
				Protocol:        "http",
			},
		},
		expectedExporters: []sdktrace.SpanExporter{&otlptrace.Exporter{}},
	}, {
		name: "invalid otel trace exporter protocol",
		tracingConfig: config.TracingSpec{
			Otel: &config.OtelSpec{
				EndpointAddress: "foo.bar",
				IsSecure:        ptr.Of(false),
				Protocol:        "tcp",
			},
		},
		expectedErr: "invalid protocol tcp provided for Otel endpoint",
	}, {
		name: "stdout trace exporter",
		tracingConfig: config.TracingSpec{
			Stdout: true,
		},
		expectedExporters: []sdktrace.SpanExporter{&diagUtils.StdoutExporter{}},
	}, {
		name: "all trace exporters",
		tracingConfig: config.TracingSpec{
			Otel: &config.OtelSpec{
				EndpointAddress: "http://foo.bar",
				IsSecure:        ptr.Of(false),
				Protocol:        "http",
			},
			Zipkin: &config.ZipkinSpec{
				EndpointAddress: "http://foo.bar",
			},
			Stdout: true,
		},
		expectedExporters: []sdktrace.SpanExporter{&diagUtils.StdoutExporter{}, &zipkin.Exporter{}, &otlptrace.Exporter{}},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rt := NewTestDaprRuntime(modes.StandaloneMode)
			defer stopRuntime(t, rt)
			rt.globalConfig.Spec.TracingSpec = &tc.tracingConfig
			if tc.hostAddress != "" {
				rt.hostAddress = tc.hostAddress
			}
			// Setup tracing with the fake tracer provider  store to confirm
			// the right exporter was registered.
			tpStore := newFakeTracerProviderStore()
			if err := rt.setupTracing(rt.hostAddress, tpStore); tc.expectedErr != "" {
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}
			if len(tc.expectedExporters) > 0 {
				assert.True(t, tpStore.HasExporter())
			}
			for i, exporter := range tpStore.exporters {
				// Exporter types don't expose internals, so we can only validate that
				// the right type of  exporter was registered.
				assert.Equal(t, reflect.TypeOf(tc.expectedExporters[i]), reflect.TypeOf(exporter))
			}
			// Setup tracing with the OpenTelemetry trace provider store.
			// We have no way to validate the result, but we can at least
			// confirm that nothing blows up.
			if tc.expectedErr == "" {
				rt.setupTracing(rt.hostAddress, newOpentelemetryTracerProviderStore())
			}
		})
	}
}

func TestMetadataUUID(t *testing.T) {
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		commonapi.NameValuePair{
			Name: "consumerID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{uuid}"),
				},
			},
		}, commonapi.NameValuePair{
			Name: "twoUUIDs",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{uuid} {uuid}"),
				},
			},
		})
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		uuid0, err := uuid.Parse(consumerID)
		assert.NoError(t, err)

		twoUUIDs := metadata.Properties["twoUUIDs"]
		uuids := strings.Split(twoUUIDs, " ")
		assert.Equal(t, 2, len(uuids))
		uuid1, err := uuid.Parse(uuids[0])
		assert.NoError(t, err)
		uuid2, err := uuid.Parse(uuids[1])
		assert.NoError(t, err)

		assert.NotEqual(t, uuid0, uuid1)
		assert.NotEqual(t, uuid0, uuid2)
		assert.NotEqual(t, uuid1, uuid2)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.NoError(t, err)
}

func TestMetadataPodName(t *testing.T) {
	t.Setenv("POD_NAME", "testPodName")

	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		commonapi.NameValuePair{
			Name: "consumerID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{podName}"),
				},
			},
		})
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]

		assert.Equal(t, "testPodName", consumerID)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.NoError(t, err)
}

func TestMetadataNamespace(t *testing.T) {
	t.Setenv("NAMESPACE", "test")

	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		commonapi.NameValuePair{
			Name: "consumerID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{namespace}"),
				},
			},
		})
	rt := NewTestDaprRuntimeWithID(modes.KubernetesMode, "app1")

	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]

		assert.Equal(t, "test.app1", consumerID)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.NoError(t, err)
}

func TestMetadataAppID(t *testing.T) {
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		commonapi.NameValuePair{
			Name: "clientID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{appID} {appID}"),
				},
			},
		})
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	rt.runtimeConfig.id = TestRuntimeConfigID
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		clientID := metadata.Properties["clientID"]
		appIds := strings.Split(clientID, " ")
		assert.Equal(t, 2, len(appIds))
		for _, appID := range appIds {
			assert.Equal(t, TestRuntimeConfigID, appID)
		}
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.NoError(t, err)
}

func TestOnComponentUpdated(t *testing.T) {
	t.Run("component spec changed, component is updated", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		rt.compStore.AddComponent(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "name1",
						Value: commonapi.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("value1"),
							},
						},
					},
				},
			},
		})

		go func() {
			<-rt.pendingComponents
		}()

		updated := rt.onComponentUpdated(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "name1",
						Value: commonapi.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("value2"),
							},
						},
					},
				},
			},
		})

		assert.True(t, updated)
	})

	t.Run("component spec unchanged, component is skipped", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		rt.compStore.AddComponent(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "name1",
						Value: commonapi.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("value1"),
							},
						},
					},
				},
			},
		})

		go func() {
			<-rt.pendingComponents
		}()

		updated := rt.onComponentUpdated(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "name1",
						Value: commonapi.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("value1"),
							},
						},
					},
				},
			},
		})

		assert.False(t, updated)
	})
}

func TestConsumerID(t *testing.T) {
	metadata := []commonapi.NameValuePair{
		{
			Name: "host",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("localhost"),
				},
			},
		},
		{
			Name: "password",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("fakePassword"),
				},
			},
		},
	}
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: metadata,
		},
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		assert.Equal(t, TestRuntimeConfigID, consumerID)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.NoError(t, err)
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	pubsubComponents := []componentsV1alpha1.Component{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: TestPubsubName,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Version:  "v1",
				Metadata: getFakeMetadataItems(),
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: TestSecondPubsubName,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub2",
				Version:  "v1",
				Metadata: getFakeMetadataItems(),
			},
		},
	}

	initMockPubSubForRuntime := func(rt *DaprRuntime) (*daprt.MockPubSub, *daprt.MockPubSub) {
		mockPubSub := new(daprt.MockPubSub)

		mockPubSub2 := new(daprt.MockPubSub)

		rt.runtimeConfig.registry.PubSubs().RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		}, "mockPubSub")
		rt.runtimeConfig.registry.PubSubs().RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub2
		}, "mockPubSub2")

		expectedMetadata := pubsub.Metadata{
			Base: mdata.Base{
				Name:       TestPubsubName,
				Properties: getFakeProperties(),
			},
		}

		mockPubSub.On("Init", expectedMetadata).Return(nil)
		mockPubSub.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

		expectedSecondPubsubMetadata := pubsub.Metadata{
			Base: mdata.Base{
				Name:       TestSecondPubsubName,
				Properties: getFakeProperties(),
			},
		}
		mockPubSub2.On("Init", expectedSecondPubsubMetadata).Return(nil)
		mockPubSub2.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		rt.compStore.SetTopicRoutes(nil)
		rt.compStore.SetSubscriptions(nil)
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}

		return mockPubSub, mockPubSub2
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

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
		sub := getSubscriptionCustom("topic0", "customroute/topic0")
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("publish adapter is nil, no pub sub component", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)
		a := rts.getPublishAdapter()
		assert.Nil(t, a)
	})

	t.Run("publish adapter not nil, with pub sub component", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)
		ps, _ := initMockPubSubForRuntime(rts)
		rts.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component: ps,
		})
		a := rts.getPublishAdapter()
		assert.NotNil(t, a)
	})

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		rts.appChannel = nil
		routes, err := rts.getTopicRoutes()
		assert.NoError(t, err)
		assert.Equal(t, 0, len(routes))
	})

	t.Run("load declarative subscription, no scopes", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		assert.NoError(t, err)
		defer cleanup()

		rts.runtimeConfig.standalone.ResourcesPath = []string{resourcesDir}
		subs := rts.getDeclarativeSubscriptions()
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 1) {
				assert.Equal(t, "myroute", subs[0].Rules[0].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
		}
	})

	t.Run("load declarative subscription, in scopes", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{TestRuntimeConfigID}

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		assert.NoError(t, err)
		defer cleanup()

		rts.runtimeConfig.standalone.ResourcesPath = []string{resourcesDir}
		subs := rts.getDeclarativeSubscriptions()
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 1) {
				assert.Equal(t, "myroute", subs[0].Rules[0].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, TestRuntimeConfigID, subs[0].Scopes[0])
		}
	})

	t.Run("load declarative subscription, not in scopes", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{"scope1"}

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		assert.NoError(t, err)
		defer cleanup()

		rts.runtimeConfig.standalone.ResourcesPath = []string{resourcesDir}
		subs := rts.getDeclarativeSubscriptions()
		assert.Len(t, subs, 0)
	})

	t.Run("test subscribe, app allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on protected topic with scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic10"}, []string{"topic11"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

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

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0", "topic1"}, []string{"topic0"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on 2 protected topics with scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic10", "topic11"}, []string{"topic10"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

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

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic3"}, []string{"topic5"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)

		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe on protected topic, no scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic12"}, []string{"topic12"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
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

		// User App subscribes 1 topics via http app channel
		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic0", "topic3"}, []string{"topic0", "topic5"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on 2 protected topics, with scopes on 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic10", "topic12"}, []string{"topic11", "topic12"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.startSubscriptions()

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test bulk publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{Component: &mockPublishPubSub{}})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		assert.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{Component: &mockPublishPubSub{}})
		res, err = rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		assert.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		assert.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		res, err = rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		assert.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			require.Nil(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Empty(t, res)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		res, err = rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Empty(t, res)
	})

	t.Run("test bulk publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			require.Nil(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Empty(t, res)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		res, err = rt.BulkPublish(&pubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []pubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		assert.NotNil(t, err)
		assert.Empty(t, res)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		assert.NoError(t, err)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		assert.NoError(t, err)
	})

	t.Run("test publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		assert.NoError(t, err)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		assert.NoError(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			require.Nil(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)
	})

	t.Run("test publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processComponentAndDependents(comp)
			require.Nil(t, err)
		}

		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
		})
		assert.NotNil(t, err)

		rt.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})
		assert.NotNil(t, err)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test protected topics, no scopes, operation not allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic2", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("test other protected topics, no allowed topics, no scopes, operation allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic2", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test protected topics, with scopes, operation allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topic in protected topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("topic in protected topics, not in publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "A", pubSub.ScopedPublishings)
		assert.True(t, a)
		b := rt.isPubSubOperationAllowed(TestPubsubName, "B", pubSub.ScopedPublishings)
		assert.False(t, b)
	})

	t.Run("topics A and B in protected topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		for name := range rt.compStore.ListPubSubs() {
			rt.compStore.DeletePubSub(name)
		}
		rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}})
		pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := rt.isPubSubOperationAllowed(TestPubsubName, "A", pubSub.ScopedPublishings)
		assert.True(t, a)
		b := rt.isPubSubOperationAllowed(TestPubsubName, "B", pubSub.ScopedPublishings)
		assert.False(t, b)
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err := rt.processComponentAndDependents(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)
	})

	t.Run("secret store is registered", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err := rt.processComponentAndDependents(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)
		store, ok := rt.compStore.GetSecretStore("kubernetesMock")
		assert.True(t, ok)
		assert.NotNil(t, store)
	})

	t.Run("get secret store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		rt.processComponentAndDependents(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})

		s, ok := rt.compStore.GetSecretStore("kubernetesMock")
		assert.True(t, ok)
		assert.NotNil(t, s)
	})
}

// Used to override log.Fatal/log.Fatalf so it doesn't cause the app to exit
type loggerNoPanic struct {
	logger.Logger
	fatalCalled int
}

func (l *loggerNoPanic) Fatal(args ...interface{}) {
	l.fatalCalled++
	l.Error(args...)
}

func (l *loggerNoPanic) Fatalf(format string, args ...interface{}) {
	l.fatalCalled++
	l.Errorf(format, args...)
}

func TestMiddlewareBuildPipeline(t *testing.T) {
	// Change the logger for these tests so it doesn't panic
	lnp := &loggerNoPanic{
		Logger: log,
	}
	oldLog := log
	log = lnp
	defer func() {
		log = oldLog
	}()

	t.Run("build when no global config are set", func(t *testing.T) {
		rt := &DaprRuntime{}

		pipeline, err := rt.buildHTTPPipelineForSpec(config.PipelineSpec{}, "test")
		require.NoError(t, err)
		assert.Empty(t, pipeline.Handlers)
	})

	t.Run("ignore component that does not exists", func(t *testing.T) {
		rt := &DaprRuntime{
			globalConfig:  &config.Configuration{},
			runtimeConfig: &internalConfig{},
			compStore:     compstore.New(),
		}

		pipeline, err := rt.buildHTTPPipelineForSpec(config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{
					Name:         "not_exists",
					Type:         "not_exists",
					Version:      "not_exists",
					SelectorSpec: config.SelectorSpec{},
				},
			},
		}, "test")
		require.NoError(t, err)
		assert.Len(t, pipeline.Handlers, 0)
	})

	compStore := compstore.New()
	compStore.AddComponent(componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw1",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	})
	compStore.AddComponent(componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw2",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	})

	t.Run("all components exists", func(t *testing.T) {
		rt := &DaprRuntime{
			globalConfig: &config.Configuration{},
			compStore:    compStore,
			runtimeConfig: &internalConfig{
				registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
					httpMiddlewareLoader.NewRegistry(),
				)),
			},
		}
		called := 0
		rt.runtimeConfig.registry.HTTPMiddlewares().RegisterComponent(
			func(_ logger.Logger) httpMiddlewareLoader.FactoryMethod {
				called++
				return func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
					return func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
					}, nil
				}
			},
			"fakemw",
		)

		pipeline, err := rt.buildHTTPPipelineForSpec(config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{
					Name:    "mymw1",
					Type:    "middleware.http.fakemw",
					Version: "v1",
				},
				{
					Name:    "mymw2",
					Type:    "middleware.http.fakemw",
					Version: "v1",
				},
			},
		}, "test")
		require.NoError(t, err)
		assert.Len(t, pipeline.Handlers, 2)
		assert.Equal(t, 2, called)
	})

	testInitFail := func(ignoreErrors bool) func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddComponent(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mymw",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "middleware.http.fakemw",
				Version: "v1",
			},
		})

		compStore.AddComponent(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "failmw",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:         "middleware.http.fakemw",
				Version:      "v1",
				IgnoreErrors: ignoreErrors,
				Metadata: []commonapi.NameValuePair{
					{Name: "fail", Value: commonapi.DynamicValue{JSON: v1.JSON{Raw: []byte("true")}}},
				},
			},
		})
		return func(t *testing.T) {
			rt := &DaprRuntime{
				compStore:    compStore,
				globalConfig: &config.Configuration{},
				meta:         meta.New(meta.Options{}),
				runtimeConfig: &internalConfig{
					registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
						httpMiddlewareLoader.NewRegistry(),
					)),
				},
			}
			called := 0
			rt.runtimeConfig.registry.HTTPMiddlewares().RegisterComponent(
				func(_ logger.Logger) httpMiddlewareLoader.FactoryMethod {
					called++
					return func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
						if metadata.Properties["fail"] == "true" {
							return nil, errors.New("simulated failure")
						}

						return func(next http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
						}, nil
					}
				},
				"fakemw",
			)

			pipeline, err := rt.buildHTTPPipelineForSpec(config.PipelineSpec{
				Handlers: []config.HandlerSpec{
					{
						Name:    "mymw",
						Type:    "middleware.http.fakemw",
						Version: "v1",
					},
					{
						Name:    "failmw",
						Type:    "middleware.http.fakemw",
						Version: "v1",
					},
				},
			}, "test")

			assert.Equal(t, 2, called)

			if ignoreErrors {
				require.NoError(t, err)
				assert.Len(t, pipeline.Handlers, 1)
			} else {
				require.Error(t, err)
				assert.ErrorContains(t, err, "dapr panicked")
				assert.Equal(t, 1, lnp.fatalCalled)

				// Reset
				lnp.fatalCalled = 0
			}
		}
	}

	t.Run("one components fails to init", testInitFail(false))
	t.Run("one components fails to init but ignoreErrors is true", testInitFail(true))
}

func TestPopulateSecretsConfiguration(t *testing.T) {
	t.Run("secret store configuration is populated", func(t *testing.T) {
		// setup
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.globalConfig.Spec.Secrets = &config.SecretsSpec{
			Scopes: []config.SecretsScope{
				{
					StoreName:     "testMock",
					DefaultAccess: "allow",
				},
			},
		}

		// act
		rt.populateSecretsConfiguration()

		// verify
		secConf, ok := rt.compStore.GetSecretsConfiguration("testMock")
		require.True(t, ok, "Expected testMock secret store configuration to be populated")
		assert.Equal(t, config.AllowAccess, secConf.DefaultAccess, "Expected default access as allow")
		assert.Empty(t, secConf.DeniedSecrets, "Expected testMock deniedSecrets to not be populated")
		assert.NotContains(t, secConf.AllowedSecrets, "Expected testMock allowedSecrets to not be populated")
	})
}

func TestProcessResourceSecrets(t *testing.T) {
	createMockBinding := func() *componentsV1alpha1.Component {
		return &componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mockBinding",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:     "bindings.mock",
				Version:  "v1",
				Metadata: []commonapi.NameValuePair{},
			},
		}
	}

	t.Run("Standalone Mode", func(t *testing.T) {
		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name: "a",
			SecretKeyRef: commonapi.SecretKeyRef{
				Key:  "key1",
				Name: "name1",
			},
		})
		mockBinding.Auth.SecretStore = secretstoresLoader.BuiltinKubernetesSecretStore

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			secretstoresLoader.BuiltinKubernetesSecretStore,
		)

		// add Kubernetes component manually
		rt.processComponentAndDependents(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: "v1",
			},
		})

		updated, unready := rt.processResourceSecrets(mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "value1", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Look up name only", func(t *testing.T) {
		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name: "a",
			SecretKeyRef: commonapi.SecretKeyRef{
				Name: "name1",
			},
		})
		mockBinding.Auth.SecretStore = "mock"

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return &rtmock.SecretStore{}
			},
			"mock",
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.processComponentAndDependents(componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.mock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)

		updated, unready := rt.processResourceSecrets(mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "value1", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Secret from env", func(t *testing.T) {
		t.Setenv("MY_ENV_VAR", "ciao mondo")

		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata, commonapi.NameValuePair{
			Name:   "a",
			EnvRef: "MY_ENV_VAR",
		})

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		updated, unready := rt.processResourceSecrets(mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "ciao mondo", mockBinding.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Disallowed env var", func(t *testing.T) {
		t.Setenv("APP_API_TOKEN", "test")
		t.Setenv("DAPR_KEY", "test")

		mockBinding := createMockBinding()
		mockBinding.Spec.Metadata = append(mockBinding.Spec.Metadata,
			commonapi.NameValuePair{
				Name:   "a",
				EnvRef: "DAPR_KEY",
			},
			commonapi.NameValuePair{
				Name:   "b",
				EnvRef: "APP_API_TOKEN",
			},
		)

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		updated, unready := rt.processResourceSecrets(mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "", mockBinding.Spec.Metadata[0].Value.String())
		assert.Equal(t, "", mockBinding.Spec.Metadata[1].Value.String())
		assert.Empty(t, unready)
	})
}

// Test that flushOutstandingComponents waits for components.
func TestFlushOutstandingComponent(t *testing.T) {
	t.Run("We can call flushOustandingComponents more than once", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		wasCalled := false
		m := NewMockKubernetesStoreWithInitCallback(func() {
			time.Sleep(100 * time.Millisecond)
			wasCalled = true
		})
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		go rt.processComponents()
		rt.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)

		// Make sure that the goroutine was restarted and can flush a second time
		wasCalled = false
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock2",
		)

		rt.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock2",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)
	})
	t.Run("flushOutstandingComponents blocks for components with outstanding dependanices", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return mc
			},
			"kubernetesMockChild",
		)
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return mgc
			},
			"kubernetesMockGrandChild",
		)

		go rt.processComponents()
		rt.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMockGrandChild",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMockGrandChild",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "a",
						SecretKeyRef: commonapi.SecretKeyRef{
							Key:  "key1",
							Name: "name1",
						},
					},
				},
			},
			Auth: componentsV1alpha1.Auth{
				SecretStore: "kubernetesMockChild",
			},
		}
		rt.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMockChild",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMockChild",
				Version: "v1",
				Metadata: []commonapi.NameValuePair{
					{
						Name: "a",
						SecretKeyRef: commonapi.SecretKeyRef{
							Key:  "key1",
							Name: "name1",
						},
					},
				},
			},
			Auth: componentsV1alpha1.Auth{
				SecretStore: "kubernetesMock",
			},
		}
		rt.pendingComponents <- componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		}
		rt.flushOutstandingComponents()
		assert.True(t, wasCalled)
		assert.True(t, wasCalledChild)
		assert.True(t, wasCalledGrandChild)
	})
}

// Test InitSecretStore if secretstore.* refers to Kubernetes secret store.
func TestInitSecretStoresInKubernetesMode(t *testing.T) {
	t.Run("built-in secret store is added", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			secretstoresLoader.BuiltinKubernetesSecretStore,
		)

		assertBuiltInSecretStore(t, rt)
	})

	t.Run("disable built-in secret store flag", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)
		rt.runtimeConfig.disableBuiltinK8sSecretStore = true

		testOk := make(chan struct{})
		defer close(testOk)
		go func() {
			// If the test fails, this call blocks forever, eventually causing a timeout
			rt.appendBuiltinSecretStore()
			testOk <- struct{}{}
		}()
		select {
		case <-testOk:
			return
		case <-time.After(5 * time.Second):
			t.Fatalf("test failed")
		}
	})

	t.Run("built-in secret store bypasses authorizers", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)
		rt.componentAuthorizers = []ComponentAuthorizer{
			func(component componentsV1alpha1.Component) bool {
				return false
			},
		}

		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			secretstoresLoader.BuiltinKubernetesSecretStore,
		)

		assertBuiltInSecretStore(t, rt)
	})
}

func assertBuiltInSecretStore(t *testing.T, rt *DaprRuntime) {
	wg := sync.WaitGroup{}
	go func() {
		for comp := range rt.pendingComponents {
			err := rt.processComponentAndDependents(comp)
			assert.NoError(t, err)
			if comp.Name == secretstoresLoader.BuiltinKubernetesSecretStore {
				wg.Done()
			}
		}
	}()
	wg.Add(1)
	rt.appendBuiltinSecretStore()
	wg.Wait()
	close(rt.pendingComponents)
}

func TestErrorPublishedNonCloudEventHTTP(t *testing.T) {
	topic := "topic1"

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: map[string]interface{}{},
		topic:      topic,
		data:       []byte("testing"),
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.topic).
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.metadata)
	defer fakeReq.Close()

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
		TestPubsubName: map[string]compstore.TopicRouteElem{
			"topic1": {
				Rules: []*runtimePubsub.Rule{{Path: "topic1"}},
			},
		},
	})

	t.Run("ok without result body", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		var appResp pubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
	})

	t.Run("ok with retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Error(t, err)
	})

	t.Run("ok with drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
	})

	t.Run("ok with unknown", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"UNKNOWN\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Error(t, err)
	})

	t.Run("not found response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
	})
}

func TestErrorPublishedNonCloudEventGRPC(t *testing.T) {
	topic := "topic1"

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: map[string]interface{}{},
		topic:      topic,
		data:       []byte("testing"),
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
		TestPubsubName: map[string]compstore.TopicRouteElem{
			"topic1": {
				Rules: []*runtimePubsub.Rule{{Path: "topic1"}},
			},
		},
	})

	testcases := []struct {
		Name        string
		Status      runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		Error       error
		ExpectError bool
	}{
		{
			Name:   "ok without success",
			Status: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			Name:        "ok with retry",
			Status:      runtimev1pb.TopicEventResponse_RETRY,
			ExpectError: true,
		},
		{
			Name:   "ok with drop",
			Status: runtimev1pb.TopicEventResponse_DROP,
		},
		{
			Name:        "ok with unknown",
			Status:      runtimev1pb.TopicEventResponse_TopicEventResponseStatus(999),
			ExpectError: true,
		},
		{
			Name:        "ok with error",
			Error:       errors.New("TEST"),
			ExpectError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			mockClientConn := channelt.MockClientConn{
				InvokeFn: func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
					if tc.Error != nil {
						return tc.Error
					}

					response, ok := reply.(*runtimev1pb.TopicEventResponse)
					if !ok {
						return fmt.Errorf("unexpected reply type: %s", reflect.TypeOf(reply))
					}

					response.Status = tc.Status

					return nil
				},
			}
			rt.grpc.SetLocalConnCreateFn(func() (grpc.ClientConnInterface, error) {
				return &mockClientConn, nil
			})

			err := rt.publishMessageGRPC(context.Background(), testPubSubMessage)
			if tc.ExpectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestOnNewPublishedMessage(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic,
		TestSecondPubsubName, "", []byte("Test Message"), "", "")
	b, err := json.Marshal(envelope)
	assert.NoError(t, err)

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       b,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.topic).
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.metadata)
	defer fakeReq.Close()

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
		TestPubsubName: map[string]compstore.TopicRouteElem{
			"topic1": {
				Rules: []*runtimePubsub.Rule{{Path: "topic1"}},
			},
		},
	})

	t.Run("succeeded to publish message to user app with empty response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		var appResp pubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message without TraceID", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		var appResp pubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		// Generate a new envelope to avoid affecting other tests by modifying shared `envelope`
		envelopeNoTraceID := pubsub.NewCloudEventsEnvelope(
			"", "", pubsub.DefaultCloudEventType, "", topic, TestSecondPubsubName, "",
			[]byte("Test Message"), "", "")
		delete(envelopeNoTraceID, pubsub.TraceIDField)
		bNoTraceID, err := json.Marshal(envelopeNoTraceID)
		assert.NoError(t, err)

		message := &pubsubSubscribedMessage{
			cloudEvent: envelopeNoTraceID,
			topic:      topic,
			data:       bNoTraceID,
			metadata:   map[string]string{pubsubName: TestPubsubName},
			path:       "topic1",
		}

		fakeReqNoTraceID := invokev1.NewInvokeMethodRequest(message.topic).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(message.data).
			WithContentType(contenttype.CloudEventContentType).
			WithCustomHTTPMetadata(testPubSubMessage.metadata)
		defer fakeReqNoTraceID.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReqNoTraceID).Return(fakeResp, nil)

		// act
		err = rt.publishMessageHTTP(context.Background(), message)

		// assert
		assert.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with non-json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with status", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"SUCCESS\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask for retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.data, &cloudEvent)
		expectedClientError := fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent["id"].(string), rterrors.NewRetriable(nil))
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask to drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned unknown status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"not_valid\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Error(t, err, "expected error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned empty status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"empty status\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err, "expected no error on empty status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app and app returned unexpected json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"success\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Nil(t, err, "expected no error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message error on invoking method", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		invokeError := errors.New("error invoking method")

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(nil, invokeError)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		expectedError := fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(invokeError))
		assert.Equal(t, expectedError.Error(), err.Error(), "expected errors to match")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 404", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil).
			WithRawDataString("Not found").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Nil(t, err, "expected error to be nil")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 500", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil).
			WithRawDataString("Internal Error").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.data, &cloudEvent)
		errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: Internal Error. status code returned: 500", cloudEvent["id"].(string), cloudEvent["topic"])
		expectedClientError := rterrors.NewRetriable(errors.New(errMsg))
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestOnNewPublishedMessageGRPC(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic,
		TestSecondPubsubName, "", []byte("Test Message"), "", "")
	// add custom attributes
	envelope["customInt"] = 123
	envelope["customString"] = "abc"
	envelope["customBool"] = true
	envelope["customFloat"] = 1.23
	envelope["customArray"] = []interface{}{"a", "b", 789, 3.1415}
	envelope["customMap"] = map[string]interface{}{"a": "b", "c": 456}
	b, err := json.Marshal(envelope)
	assert.NoError(t, err)

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       b,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	envelope = pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic,
		TestSecondPubsubName, "application/octet-stream", []byte{0x1}, "", "")
	// add custom attributes
	envelope["customInt"] = 123
	envelope["customString"] = "abc"
	envelope["customBool"] = true
	envelope["customFloat"] = 1.23
	envelope["customArray"] = []interface{}{"a", "b", 789, 3.1415}
	envelope["customMap"] = map[string]interface{}{"a": "b", "c": 456}
	base64, err := json.Marshal(envelope)
	assert.NoError(t, err)

	testPubSubMessageBase64 := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       base64,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	testCases := []struct {
		name                        string
		message                     *pubsubSubscribedMessage
		responseStatus              runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		expectedError               error
		noResponseStatus            bool
		responseError               error
		validateCloudEventExtension *map[string]interface{}
	}{
		{
			name:             "failed to publish message to user app with unimplemented error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    status.Errorf(codes.Unimplemented, "unimplemented method"),
		},
		{
			name:             "failed to publish message to user app with response error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    assert.AnError,
			expectedError: fmt.Errorf(
				"error returned from app while processing pub/sub event %v: %w",
				testPubSubMessage.cloudEvent[pubsub.IDField],
				rterrors.NewRetriable(status.Error(codes.Unknown, assert.AnError.Error())),
			),
		},
		{
			name:             "succeeded to publish message to user app with empty response",
			message:          testPubSubMessage,
			noResponseStatus: true,
		},
		{
			name:           "succeeded to publish message to user app with success response",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			name:           "succeeded to publish message to user app with base64 encoded cloud event",
			message:        testPubSubMessageBase64,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			name:           "succeeded to publish message to user app with retry",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_RETRY,
			expectedError: fmt.Errorf(
				"RETRY status returned from app while processing pub/sub event %v: %w",
				testPubSubMessage.cloudEvent[pubsub.IDField],
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:           "succeeded to publish message to user app with drop",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_DROP,
		},
		{
			name:           "succeeded to publish message to user app with invalid response",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_TopicEventResponseStatus(99),
			expectedError: fmt.Errorf(
				"unknown status returned from app while processing pub/sub event %v, status: %v, err: %w",
				testPubSubMessage.cloudEvent[pubsub.IDField],
				runtimev1pb.TopicEventResponse_TopicEventResponseStatus(99),
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:           "succeeded to publish message to user app and validated cloud event extension attributes",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
			}),
		},
		{
			name:           "succeeded to publish message to user app and validated cloud event extension attributes with base64 encoded data",
			message:        testPubSubMessageBase64,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// setup
			// getting new port for every run to avoid conflict and timing issues between tests if sharing same port
			port, _ := freeport.GetFreePort()
			rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, string(protocol.GRPCProtocol), port)
			rt.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
				TestPubsubName: map[string]compstore.TopicRouteElem{
					topic: {
						Rules: []*runtimePubsub.Rule{{Path: topic}},
					},
				},
			})

			var grpcServer *grpc.Server

			// create mock application server first
			if !tc.noResponseStatus {
				grpcServer = startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					TopicEventResponseStatus:    tc.responseStatus,
					Error:                       tc.responseError,
					ValidateCloudEventExtension: tc.validateCloudEventExtension,
				})
			} else {
				grpcServer = startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					Error:                       tc.responseError,
					ValidateCloudEventExtension: tc.validateCloudEventExtension,
				})
			}
			if grpcServer != nil {
				// properly stop the gRPC server
				defer grpcServer.Stop()
			}

			// create a new AppChannel and gRPC client for every test
			rt.createChannels()
			// properly close the app channel created
			defer rt.grpc.CloseAppClient()

			// act
			err = rt.publishMessageGRPC(context.Background(), tc.message)

			// assert
			if tc.expectedError != nil {
				assert.Equal(t, err.Error(), tc.expectedError.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPubsubLifecycle(t *testing.T) {
	rt := newDaprRuntime(&internalConfig{
		registry: registry.New(registry.NewOptions()),
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	defer func() {
		if rt != nil {
			stopRuntime(t, rt)
		}
	}()

	comp1 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub1",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubAlpha",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}
	comp2 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub2",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubBeta",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}
	comp3 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub3",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubBeta",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}

	rt.runtimeConfig.registry.PubSubs().RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
		c := new(daprt.InMemoryPubsub)
		c.On("Init", mock.AnythingOfType("pubsub.Metadata")).Return(nil)
		return c
	}, "mockPubSubAlpha")
	rt.runtimeConfig.registry.PubSubs().RegisterComponent(func(_ logger.Logger) pubsub.PubSub {
		c := new(daprt.InMemoryPubsub)
		c.On("Init", mock.AnythingOfType("pubsub.Metadata")).Return(nil)
		return c
	}, "mockPubSubBeta")

	err := rt.processComponentAndDependents(comp1)
	assert.NoError(t, err)
	err = rt.processComponentAndDependents(comp2)
	assert.NoError(t, err)
	err = rt.processComponentAndDependents(comp3)
	assert.NoError(t, err)

	forEachPubSub := func(f func(name string, comp *daprt.InMemoryPubsub)) int {
		i := 0
		for name, ps := range rt.compStore.ListPubSubs() {
			f(name, ps.Component.(*daprt.InMemoryPubsub))
			i++
		}
		return i
	}
	getPubSub := func(name string) *daprt.InMemoryPubsub {
		pubSub, ok := rt.compStore.GetPubSub(name)
		require.True(t, ok)
		return pubSub.Component.(*daprt.InMemoryPubsub)
	}

	done := forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
		comp.AssertNumberOfCalls(t, "Init", 1)
	})
	require.Equal(t, 3, done)

	subscriptions := make(map[string][]string)
	messages := make(map[string][]*pubsub.NewMessage)
	var (
		subscriptionsCh  chan struct{}
		messagesCh       chan struct{}
		subscriptionsMux sync.Mutex
		msgMux           sync.Mutex
	)
	forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
		comp.SetOnSubscribedTopicsChanged(func(topics []string) {
			sort.Strings(topics)
			subscriptionsMux.Lock()
			subscriptions[name] = topics
			subscriptionsMux.Unlock()
			if subscriptionsCh != nil {
				subscriptionsCh <- struct{}{}
			}
		})
		comp.SetHandler(func(topic string, msg *pubsub.NewMessage) {
			msgMux.Lock()
			if messages[name+"|"+topic] == nil {
				messages[name+"|"+topic] = []*pubsub.NewMessage{msg}
			} else {
				messages[name+"|"+topic] = append(messages[name+"|"+topic], msg)
			}
			msgMux.Unlock()
			if messagesCh != nil {
				messagesCh <- struct{}{}
			}
		})
	})

	setTopicRoutes := func() {
		rt.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"mockPubSub1": {
				"topic1": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic1"}},
				},
			},
			"mockPubSub2": {
				"topic2": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic2"}},
				},
				"topic3": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic3"}},
				},
			},
		})

		forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			comp.Calls = nil
			comp.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
		})
	}

	resetState := func() {
		messages = make(map[string][]*pubsub.NewMessage)
		forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			comp.Calls = nil
		})
	}

	subscribePredefined := func(t *testing.T) {
		setTopicRoutes()

		subscriptionsCh = make(chan struct{}, 5)
		rt.startSubscriptions()

		done := forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			switch name {
			case "mockPubSub1":
				comp.AssertNumberOfCalls(t, "Subscribe", 1)
			case "mockPubSub2":
				comp.AssertNumberOfCalls(t, "Subscribe", 2)
			case "mockPubSub3":
				comp.AssertNumberOfCalls(t, "Subscribe", 0)
			}
		})
		require.Equal(t, 3, done)

		for i := 0; i < 3; i++ {
			<-subscriptionsCh
		}
		close(subscriptionsCh)
		subscriptionsCh = nil

		assert.True(t, reflect.DeepEqual(subscriptions["mockPubSub1"], []string{"topic1"}),
			"expected %v to equal %v", subscriptions["mockPubSub1"], []string{"topic1"})
		assert.True(t, reflect.DeepEqual(subscriptions["mockPubSub2"], []string{"topic2", "topic3"}),
			"expected %v to equal %v", subscriptions["mockPubSub2"], []string{"topic2", "topic3"})
		assert.Len(t, subscriptions["mockPubSub3"], 0)
	}

	t.Run("subscribe to 3 topics on 2 components", subscribePredefined)

	ciao := []byte("ciao")
	sendMessages := func(t *testing.T, expect int) {
		send := []pubsub.PublishRequest{
			{Data: ciao, PubsubName: "mockPubSub1", Topic: "topic1"},
			{Data: ciao, PubsubName: "mockPubSub2", Topic: "topic2"},
			{Data: ciao, PubsubName: "mockPubSub2", Topic: "topic3"},
			{Data: ciao, PubsubName: "mockPubSub3", Topic: "topic4"},
			{Data: ciao, PubsubName: "mockPubSub3", Topic: "not-subscribed"},
		}

		messagesCh = make(chan struct{}, expect+2)

		for _, m := range send {
			//nolint:gosec
			err = rt.Publish(&m)
			assert.NoError(t, err)
		}

		for i := 0; i < expect; i++ {
			<-messagesCh
		}

		// Sleep to ensure no new messages have come in
		time.Sleep(10 * time.Millisecond)
		assert.Len(t, messagesCh, 0)

		close(messagesCh)
		messagesCh = nil
	}

	t.Run("messages are delivered to 3 topics", func(t *testing.T) {
		resetState()
		sendMessages(t, 3)

		require.Len(t, messages, 3)
		_ = assert.Len(t, messages["mockPubSub1|topic1"], 1) &&
			assert.Equal(t, messages["mockPubSub1|topic1"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic2"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic2"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
	})

	t.Run("unsubscribe from mockPubSub2/topic2", func(t *testing.T) {
		resetState()
		comp := getPubSub("mockPubSub2")
		comp.On("unsubscribed", "topic2").Return(nil).Once()

		err = rt.unsubscribeTopic("mockPubSub2", "topic2")
		require.NoError(t, err)

		sendMessages(t, 2)

		require.Len(t, messages, 2)
		_ = assert.Len(t, messages["mockPubSub1|topic1"], 1) &&
			assert.Equal(t, messages["mockPubSub1|topic1"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		comp.AssertCalled(t, "unsubscribed", "topic2")
	})

	t.Run("unsubscribe from mockPubSub1/topic1", func(t *testing.T) {
		resetState()
		comp := getPubSub("mockPubSub1")
		comp.On("unsubscribed", "topic1").Return(nil).Once()

		err = rt.unsubscribeTopic("mockPubSub1", "topic1")
		require.NoError(t, err)

		sendMessages(t, 1)

		require.Len(t, messages, 1)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		comp.AssertCalled(t, "unsubscribed", "topic1")
	})

	t.Run("subscribe to mockPubSub3/topic4", func(t *testing.T) {
		resetState()

		err = rt.subscribeTopic(rt.pubsubCtx, "mockPubSub3", "topic4", compstore.TopicRouteElem{})
		require.NoError(t, err)

		sendMessages(t, 2)

		require.Len(t, messages, 2)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub3|topic4"], 1) &&
			assert.Equal(t, messages["mockPubSub3|topic4"][0].Data, ciao)
	})

	t.Run("stopSubscriptions", func(t *testing.T) {
		resetState()
		comp2 := getPubSub("mockPubSub2")
		comp2.On("unsubscribed", "topic3").Return(nil).Once()
		comp3 := getPubSub("mockPubSub3")
		comp3.On("unsubscribed", "topic4").Return(nil).Once()

		rt.stopSubscriptions()

		sendMessages(t, 0)

		require.Len(t, messages, 0)
		comp2.AssertCalled(t, "unsubscribed", "topic3")
		comp3.AssertCalled(t, "unsubscribed", "topic4")
	})

	t.Run("restart subscriptions with pre-defined ones", subscribePredefined)

	t.Run("shutdown", func(t *testing.T) {
		resetState()

		comp1 := getPubSub("mockPubSub1")
		comp1.On("unsubscribed", "topic1").Return(nil).Once()
		comp2 := getPubSub("mockPubSub2")
		comp2.On("unsubscribed", "topic2").Return(nil).Once()
		comp2.On("unsubscribed", "topic3").Return(nil).Once()

		stopRuntime(t, rt)
		rt = nil

		comp1.AssertCalled(t, "unsubscribed", "topic1")
		comp2.AssertCalled(t, "unsubscribed", "topic2")
		comp2.AssertCalled(t, "unsubscribed", "topic3")
	})
}

func TestPubsubWithResiliency(t *testing.T) {
	r := newDaprRuntime(&internalConfig{
		registry: registry.New(registry.NewOptions()),
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency))
	defer stopRuntime(t, r)

	failingPubsub := daprt.FailingPubsub{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingTopic": 1,
			},
			map[string]time.Duration{
				"timeoutTopic": time.Second * 10,
			},
			map[string]int{},
		),
	}

	failingAppChannel := daprt.FailingAppChannel{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingSubTopic": 1,
			},
			map[string]time.Duration{
				"timeoutSubTopic": time.Second * 10,
			},
			map[string]int{},
		),
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			rawData, _ := io.ReadAll(req.RawData())
			data := make(map[string]string)
			json.Unmarshal(rawData, &data)
			val, _ := base64.StdEncoding.DecodeString(data["data_base64"])
			return string(val)
		},
	}

	r.runtimeConfig.registry.PubSubs().RegisterComponent(func(_ logger.Logger) pubsub.PubSub { return &failingPubsub }, "failingPubsub")

	component := componentsV1alpha1.Component{}
	component.ObjectMeta.Name = "failPubsub"
	component.Spec.Type = "pubsub.failingPubsub"

	err := r.processor.One(context.TODO(), component)
	assert.NoError(t, err)

	t.Run("pubsub publish retries with resiliency", func(t *testing.T) {
		req := &pubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "failingTopic",
		}
		err := r.Publish(req)

		assert.NoError(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("failingTopic"))
	})

	t.Run("pubsub publish times out with resiliency", func(t *testing.T) {
		req := &pubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "timeoutTopic",
		}

		start := time.Now()
		err := r.Publish(req)
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("timeoutTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	r.runtimeConfig.appConnectionConfig.Protocol = protocol.HTTPProtocol
	r.appChannel = &failingAppChannel

	t.Run("pubsub retries subscription event with resiliency", func(t *testing.T) {
		r.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"failPubsub": map[string]compstore.TopicRouteElem{
				"failingSubTopic": {
					Metadata: map[string]string{
						"rawPayload": "true",
					},
					Rules: []*runtimePubsub.Rule{
						{
							Path: "failingPubsub",
						},
					},
				},
			},
		})

		for name := range r.compStore.ListPubSubs() {
			r.compStore.DeletePubSub(name)
		}
		r.compStore.AddPubSub("failPubsub", compstore.PubsubItem{Component: &failingPubsub})

		r.topicCtxCancels = map[string]context.CancelFunc{}
		r.pubsubCtx, r.pubsubCancel = context.WithCancel(context.Background())
		defer r.pubsubCancel()
		err := r.beginPubSub("failPubsub")

		assert.NoError(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount("failingSubTopic"))
	})

	t.Run("pubsub times out sending event to app with resiliency", func(t *testing.T) {
		r.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"failPubsub": map[string]compstore.TopicRouteElem{
				"timeoutSubTopic": {
					Metadata: map[string]string{
						"rawPayload": "true",
					},
					Rules: []*runtimePubsub.Rule{
						{
							Path: "failingPubsub",
						},
					},
				},
			},
		})

		for name := range r.compStore.ListPubSubs() {
			r.compStore.DeletePubSub(name)
		}
		r.compStore.AddPubSub("failPubsub", compstore.PubsubItem{Component: &failingPubsub})

		r.topicCtxCancels = map[string]context.CancelFunc{}
		r.pubsubCtx, r.pubsubCancel = context.WithCancel(context.Background())
		defer r.pubsubCancel()
		start := time.Now()
		err := r.beginPubSub("failPubsub")
		end := time.Now()

		// This is eaten, technically.
		assert.NoError(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount("timeoutSubTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

// mockSubscribePubSub is an in-memory pubsub component.
type mockSubscribePubSub struct {
	bulkHandlers    map[string]pubsub.BulkHandler
	handlers        map[string]pubsub.Handler
	pubCount        map[string]int
	bulkPubCount    map[string]int
	isBulkSubscribe bool
	bulkReponse     pubsub.BulkSubscribeResponse
	features        []pubsub.Feature
}

// type BulkSubscribeResponse struct {

// Init is a mock initialization method.
func (m *mockSubscribePubSub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	m.bulkHandlers = make(map[string]pubsub.BulkHandler)
	m.handlers = make(map[string]pubsub.Handler)
	m.pubCount = make(map[string]int)
	m.bulkPubCount = make(map[string]int)
	return nil
}

// Publish is a mock publish method. Immediately trigger handler if topic is subscribed.
func (m *mockSubscribePubSub) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	m.pubCount[req.Topic]++
	var err error
	if handler, ok := m.handlers[req.Topic]; ok {
		pubsubMsg := &pubsub.NewMessage{
			Data:  req.Data,
			Topic: req.Topic,
		}
		handler(context.Background(), pubsubMsg)
	} else if bulkHandler, ok := m.bulkHandlers[req.Topic]; ok {
		m.bulkPubCount[req.Topic]++
		nbei := pubsub.BulkMessageEntry{
			EntryId: "0",
			Event:   req.Data,
		}
		msgArr := []pubsub.BulkMessageEntry{nbei}
		nbm := &pubsub.BulkMessage{
			Entries: msgArr,
			Topic:   req.Topic,
		}
		_, err = bulkHandler(context.Background(), nbm)
	}
	return err
}

// BulkPublish is a mock publish method. Immediately call the handler for each event in request if topic is subscribed.
func (m *mockSubscribePubSub) BulkPublish(_ context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	m.bulkPubCount[req.Topic]++
	res := pubsub.BulkPublishResponse{}
	if handler, ok := m.handlers[req.Topic]; ok {
		for _, entry := range req.Entries {
			m.pubCount[req.Topic]++
			// TODO this needs to be modified as part of BulkSubscribe deadletter test
			pubsubMsg := &pubsub.NewMessage{
				Data:  entry.Event,
				Topic: req.Topic,
			}
			handler(context.Background(), pubsubMsg)
		}
	} else if bulkHandler, ok := m.bulkHandlers[req.Topic]; ok {
		nbm := &pubsub.BulkMessage{
			Entries: req.Entries,
			Topic:   req.Topic,
		}
		bulkResponses, err := bulkHandler(context.Background(), nbm)
		m.bulkReponse.Statuses = bulkResponses
		m.bulkReponse.Error = err
	}

	return res, nil
}

// Subscribe is a mock subscribe method.
func (m *mockSubscribePubSub) Subscribe(_ context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	m.handlers[req.Topic] = handler
	return nil
}

func (m *mockSubscribePubSub) Close() error {
	return nil
}

func (m *mockSubscribePubSub) Features() []pubsub.Feature {
	return m.features
}

func (m *mockSubscribePubSub) BulkSubscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) error {
	m.isBulkSubscribe = true
	m.bulkHandlers[req.Topic] = handler
	return nil
}

func (m *mockSubscribePubSub) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func (m *mockSubscribePubSub) GetBulkResponse() pubsub.BulkSubscribeResponse {
	return m.bulkReponse
}

func TestPubSubDeadLetter(t *testing.T) {
	testDeadLetterPubsub := "failPubsub"
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDeadLetterPubsub,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	t.Run("succeeded to publish message to dead letter when send message to app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.runtimeConfig.registry.PubSubs().RegisterComponent(
			func(_ logger.Logger) pubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).
			Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).
			Return(nil, errors.New("failed to send"))

		require.NoError(t, rt.processor.One(context.TODO(), pubsubComponent))
		rt.startSubscriptions()

		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		assert.NoError(t, err)
		pubSub, ok := rt.compStore.GetPubSub(testDeadLetterPubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.pubCount["topic0"])
		// Ensure the message is sent to dead letter topic.
		assert.Equal(t, 1, pubsubIns.pubCount["topic1"])
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
	})

	t.Run("use dead letter with resiliency", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.resiliency = resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency)
		rt.runtimeConfig.registry.PubSubs().RegisterComponent(
			func(_ logger.Logger) pubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).
			Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).
			Return(nil, errors.New("failed to send"))

		require.NoError(t, rt.processor.One(context.TODO(), pubsubComponent))
		rt.startSubscriptions()

		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		assert.NoError(t, err)
		pubSub, ok := rt.compStore.GetPubSub(testDeadLetterPubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		// Consider of resiliency, publish message may retry in some cases, make sure the pub count is greater than 1.
		assert.True(t, pubsubIns.pubCount["topic0"] >= 1)
		// Make sure every message that is sent to topic0 is sent to its dead letter topic1.
		assert.Equal(t, pubsubIns.pubCount["topic0"], pubsubIns.pubCount["topic1"])
		// Except of the one getting config from app, make sure each publish will result to twice subscribe call
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1+2*pubsubIns.pubCount["topic0"]+2*pubsubIns.pubCount["topic1"])
	})
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
			rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, string(protocol.GRPCProtocol), port)
			// create mock application server first
			grpcServer := startTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
				Error:    tc.responseError,
				Bindings: tc.responseFromApp,
			})
			defer grpcServer.Stop()

			// create a new AppChannel and gRPC client for every test
			rt.createChannels()
			// properly close the app channel created
			defer rt.grpc.CloseAppClient()

			// act
			resp, _ := rt.getSubscribedBindingsGRPC()

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
		scopes.SubscriptionScopes: fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID),
		scopes.PublishingScopes:   fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID),
		scopes.ProtectedTopics:    "topic10,topic11,topic12",
	}
}

func getFakeMetadataItems() []commonapi.NameValuePair {
	return []commonapi.NameValuePair{
		{
			Name: "host",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("localhost"),
				},
			},
		},
		{
			Name: "password",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("fakePassword"),
				},
			},
		},
		{
			Name: "consumerID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(TestRuntimeConfigID),
				},
			},
		},
		{
			Name: scopes.SubscriptionScopes,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID)),
				},
			},
		},
		{
			Name: scopes.PublishingScopes,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID)),
				},
			},
		},
		{
			Name: scopes.ProtectedTopics,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("topic10,topic11,topic12"),
				},
			},
		},
	}
}

func NewTestDaprRuntime(mode modes.DaprMode) *DaprRuntime {
	return NewTestDaprRuntimeWithProtocol(mode, string(protocol.HTTPProtocol), 1024)
}

func NewTestDaprRuntimeWithID(mode modes.DaprMode, id string) *DaprRuntime {
	testRuntimeConfig := NewTestDaprRuntimeConfig(mode, string(protocol.HTTPProtocol), 1024)
	testRuntimeConfig.id = id
	rt := newDaprRuntime(testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	return rt
}

func NewTestDaprRuntimeWithProtocol(mode modes.DaprMode, protocol string, appPort int) *DaprRuntime {
	testRuntimeConfig := NewTestDaprRuntimeConfig(mode, protocol, appPort)
	rt := newDaprRuntime(testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	return rt
}

func NewTestDaprRuntimeConfig(mode modes.DaprMode, appProtocol string, appPort int) *internalConfig {
	return &internalConfig{
		id:                 TestRuntimeConfigID,
		placementAddresses: []string{"10.10.10.12"},
		kubernetes: modeconfig.KubernetesConfig{
			ControlPlaneAddress: "10.10.10.11",
		},
		allowedOrigins: cors.DefaultAllowedOrigins,
		appConnectionConfig: config.AppConnectionConfig{
			Protocol:       protocol.Protocol(appProtocol),
			Port:           appPort,
			MaxConcurrency: -1,
			ChannelAddress: "127.0.0.1",
		},
		mode:                         mode,
		httpPort:                     DefaultDaprHTTPPort,
		internalGRPCPort:             0,
		apiGRPCPort:                  DefaultDaprAPIGRPCPort,
		apiListenAddresses:           []string{DefaultAPIListenAddress},
		publicPort:                   nil,
		profilePort:                  DefaultProfilePort,
		enableProfiling:              false,
		mTLSEnabled:                  false,
		sentryServiceAddress:         "",
		maxRequestBodySize:           4,
		unixDomainSocket:             "",
		readBufferSize:               4,
		gracefulShutdownDuration:     time.Second,
		enableAPILogging:             ptr.Of(true),
		disableBuiltinK8sSecretStore: false,
		registry: registry.New(registry.NewOptions().
			WithStateStores(stateLoader.NewRegistry()).
			WithSecretStores(secretstoresLoader.NewRegistry()).
			WithNameResolutions(nrLoader.NewRegistry()).
			WithBindings(bindingsLoader.NewRegistry()).
			WithPubSubs(pubsubLoader.NewRegistry()).
			WithHTTPMiddlewares(httpMiddlewareLoader.NewRegistry()).
			WithConfigurations(configurationLoader.NewRegistry()).
			WithLocks(lockLoader.NewRegistry())),
	}
}

func TestGracefulShutdown(t *testing.T) {
	r := NewTestDaprRuntime(modes.StandaloneMode)
	assert.Equal(t, time.Second, r.runtimeConfig.gracefulShutdownDuration)
}

func TestMTLS(t *testing.T) {
	t.Run("with mTLS enabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.runtimeConfig.mTLSEnabled = true
		rt.runtimeConfig.sentryServiceAddress = "1.1.1.1"

		t.Setenv(sentryConsts.TrustAnchorsEnvVar, testCertRoot)
		t.Setenv(sentryConsts.CertChainEnvVar, "a")
		t.Setenv(sentryConsts.CertKeyEnvVar, "b")

		certChain, err := security.GetCertChain()
		assert.NoError(t, err)
		rt.runtimeConfig.certChain = certChain

		err = rt.establishSecurity(rt.runtimeConfig.sentryServiceAddress)
		assert.NoError(t, err)
		assert.NotNil(t, rt.authenticator)
	})

	t.Run("with mTLS disabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		err := rt.establishSecurity(rt.runtimeConfig.sentryServiceAddress)
		assert.NoError(t, err)
		assert.Nil(t, rt.authenticator)
	})

	t.Run("mTLS disabled, operator fails without TLS certs", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		_, err := rt.getOperatorClient()
		assert.Error(t, err)
	})
}

type mockBinding struct {
	readErrorCh chan bool
	data        string
	metadata    map[string]string
	closeErr    error
}

func (b *mockBinding) Init(ctx context.Context, metadata bindings.Metadata) error {
	return nil
}

func (b *mockBinding) Read(ctx context.Context, handler bindings.Handler) error {
	b.data = string(testInputBindingData)
	metadata := map[string]string{}
	if b.metadata != nil {
		metadata = b.metadata
	}

	go func() {
		_, err := handler(context.Background(), &bindings.ReadResponse{
			Metadata: metadata,
			Data:     []byte(b.data),
		})
		if b.readErrorCh != nil {
			b.readErrorCh <- (err != nil)
		}
	}()

	return nil
}

func (b *mockBinding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{bindings.CreateOperation, bindings.ListOperation}
}

func (b *mockBinding) Invoke(ctx context.Context, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	return nil, nil
}

func (b *mockBinding) Close() error {
	return b.closeErr
}

func (b *mockBinding) GetComponentMetadata() map[string]string {
	return b.metadata
}

func TestInvokeOutputBindings(t *testing.T) {
	t.Run("output binding missing operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data: []byte(""),
		})
		assert.NotNil(t, err)
		assert.Equal(t, "operation field is missing from request", err.Error())
	})

	t.Run("output binding valid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.compStore.AddOutputBinding("mockBinding", &mockBinding{})

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.CreateOperation,
		})
		assert.NoError(t, err)
	})

	t.Run("output binding invalid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.compStore.AddOutputBinding("mockBinding", &mockBinding{})

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.GetOperation,
		})
		assert.NotNil(t, err)
		assert.Equal(t, "binding mockBinding does not support operation get. supported operations:create list", err.Error())
	})
}

func TestReadInputBindings(t *testing.T) {
	const testInputBindingName = "inputbinding"
	const testInputBindingMethod = "inputbinding"

	t.Run("app acknowledge, no retry", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(testInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod(testInputBindingMethod)).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		rt.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		b := mockBinding{}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		b.readErrorCh = ch
		rt.readFromBinding(ctx, testInputBindingName, &b)
		cancel()

		assert.False(t, <-ch)
	})

	t.Run("app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer fakeBindingReq.Close()

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(testInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil).
			WithRawDataString("Internal Error").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeBindingReq).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel
		rt.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		b := mockBinding{}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		b.readErrorCh = ch
		rt.readFromBinding(ctx, testInputBindingName, &b)
		cancel()

		assert.True(t, <-ch)
	})

	t.Run("binding has data and metadata", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeBindingReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodOptions, "").
			WithContentType(invokev1.JSONContentType)
		defer fakeBindingReq.Close()

		fakeBindingResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeBindingResp.Close()

		fakeReq := invokev1.NewInvokeMethodRequest(testInputBindingMethod).
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(testInputBindingData).
			WithContentType("application/json").
			WithMetadata(map[string][]string{"bindings": {"input"}})
		defer fakeReq.Close()

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod(testInputBindingMethod)).Return(fakeBindingResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel
		rt.compStore.AddInputBindingRoute(testInputBindingName, testInputBindingName)

		b := mockBinding{metadata: map[string]string{"bindings": "input"}}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		ch := make(chan bool, 1)
		b.readErrorCh = ch
		rt.readFromBinding(ctx, testInputBindingName, &b)
		cancel()

		assert.Equal(t, string(testInputBindingData), b.data)
	})

	t.Run("start and stop reading", func(t *testing.T) {
		rt := newDaprRuntime(&internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, rt)

		closeCh := make(chan struct{})
		defer close(closeCh)

		b := &daprt.MockBinding{}
		b.SetOnReadCloseCh(closeCh)
		b.On("Read", mock.MatchedBy(matchContextInterface), mock.Anything).Return(nil).Once()

		ctx, cancel := context.WithCancel(context.Background())
		rt.readFromBinding(ctx, testInputBindingName, b)
		time.Sleep(80 * time.Millisecond)
		cancel()
		select {
		case <-closeCh:
			// All good
		case <-time.After(time.Second):
			t.Fatal("timeout while waiting for binding to stop reading")
		}

		b.AssertNumberOfCalls(t, "Read", 1)
	})
}

func TestNamespace(t *testing.T) {
	t.Run("empty namespace", func(t *testing.T) {
		assert.Empty(t, getNamespace())
	})

	t.Run("non-empty namespace", func(t *testing.T) {
		t.Setenv("NAMESPACE", "a")
		assert.Equal(t, "a", getNamespace())
	})
}

func TestPodName(t *testing.T) {
	t.Run("empty podName", func(t *testing.T) {
		assert.Empty(t, getPodName())
	})

	t.Run("non-empty podName", func(t *testing.T) {
		t.Setenv("POD_NAME", "testPodName")
		assert.Equal(t, "testPodName", getPodName())
	})
}

func TestAuthorizedComponents(t *testing.T) {
	testCompName := "fakeComponent"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{TestRuntimeConfigID}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{"other"}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{TestRuntimeConfigID}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("no authorizers", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.componentAuthorizers = []ComponentAuthorizer{}
		// Namespace mismatch, should be accepted anyways
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("only deny all", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.componentAuthorizers = []ComponentAuthorizer{
			func(component componentsV1alpha1.Component) bool {
				return false
			},
		}

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("additional authorizer denies all", func(t *testing.T) {
		cfg := NewTestDaprRuntimeConfig(modes.StandaloneMode, string(protocol.HTTPSProtocol), 1024)
		rt := newDaprRuntime(cfg, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		rt.componentAuthorizers = append(rt.componentAuthorizers, func(component componentsV1alpha1.Component) bool {
			return false
		})
		defer stopRuntime(t, rt)

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})
}

func TestAuthorizedHTTPEndpoints(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	endpoint := createTestEndpoint("testEndpoint", "http://api.test.com")

	t.Run("standalone mode, no namespace", func(t *testing.T) {
		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
		assert.Equal(t, endpoint.Name, endpoints[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("namespace match", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{TestRuntimeConfigID}

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{"other"}

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{TestRuntimeConfigID}

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{"other"}

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("no authorizers", func(t *testing.T) {
		rt.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{}
		// Namespace mismatch, should be accepted anyways
		rt.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := rt.getAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, rt.isObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
		assert.Equal(t, endpoint.Name, endpoints[0].ObjectMeta.Name)
	})
}

type mockPublishPubSub struct {
	PublishedRequest atomic.Pointer[pubsub.PublishRequest]
}

// Init is a mock initialization method.
func (m *mockPublishPubSub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method.
func (m *mockPublishPubSub) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	m.PublishedRequest.Store(req)
	return nil
}

// BulkPublish is a mock bulk publish method returning a success all the time.
func (m *mockPublishPubSub) BulkPublish(req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	return pubsub.BulkPublishResponse{}, nil
}

func (m *mockPublishPubSub) BulkSubscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) (pubsub.BulkSubscribeResponse, error) {
	return pubsub.BulkSubscribeResponse{}, nil
}

// Subscribe is a mock subscribe method.
func (m *mockPublishPubSub) Subscribe(_ context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	return nil
}

func (m *mockPublishPubSub) Close() error {
	return nil
}

func (m *mockPublishPubSub) Features() []pubsub.Feature {
	return nil
}

func (m *mockPublishPubSub) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func TestInitActors(t *testing.T) {
	t.Run("missing namespace on kubernetes", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			mode:     modes.KubernetesMode,
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
		r.namespace = ""
		r.runtimeConfig.mTLSEnabled = true

		err := r.initActors()
		assert.Error(t, err)
	})

	t.Run("actors hosted = true", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			mode:     modes.KubernetesMode,
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
		r.appConfig = config.ApplicationConfig{
			Entities: []string{"actor1"},
		}

		hosted := len(r.appConfig.Entities) > 0
		assert.True(t, hosted)
	})

	t.Run("actors hosted = false", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			mode:     modes.KubernetesMode,
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		hosted := len(r.appConfig.Entities) > 0
		assert.False(t, hosted)
	})

	t.Run("placement enable = false", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		err := r.initActors()
		assert.NotNil(t, err)
	})

	t.Run("the state stores can still be initialized normally", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		assert.Nil(t, r.actor)
		assert.NotNil(t, r.compStore.ListStateStores())
		assert.Equal(t, 0, r.compStore.StateStoresLen())
	})

	t.Run("the actor store can not be initialized normally", func(t *testing.T) {
		r := newDaprRuntime(&internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		name, ok := r.processor.ActorStateStore()
		assert.False(t, ok)
		assert.Equal(t, "", name)
		err := r.initActors()
		assert.NotNil(t, err)
	})
}

func TestBindingTracingHttp(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	t.Run("traceparent passed through with response status code 200", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		rt.appChannel = mockAppChannel

		_, err := rt.sendBindingEventToApp("mockBinding", []byte(""), map[string]string{"traceparent": "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01"})
		assert.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
		req := mockAppChannel.Calls[0].Arguments.Get(1).(*invokev1.InvokeMethodRequest)
		assert.Contains(t, req.Metadata(), "traceparent")
		assert.Contains(t, req.Metadata()["traceparent"].Values, "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01")
	})

	t.Run("traceparent passed through with response status code 204", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(204, "OK", nil), nil)
		rt.appChannel = mockAppChannel

		_, err := rt.sendBindingEventToApp("mockBinding", []byte(""), map[string]string{"traceparent": "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01"})
		assert.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
		req := mockAppChannel.Calls[0].Arguments.Get(1).(*invokev1.InvokeMethodRequest)
		assert.Contains(t, req.Metadata(), "traceparent")
		assert.Contains(t, req.Metadata()["traceparent"].Values, "00-d97eeaf10b4d00dc6ba794f3a41c5268-09462d216dd14deb-01")
	})

	t.Run("bad traceparent does not fail request", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		rt.appChannel = mockAppChannel

		_, err := rt.sendBindingEventToApp("mockBinding", []byte(""), map[string]string{"traceparent": "I am not a traceparent"})
		assert.NoError(t, err)
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
		assert.Len(t, mockAppChannel.Calls, 1)
	})
}

func TestBindingResiliency(t *testing.T) {
	r := newDaprRuntime(&internalConfig{
		registry: registry.New(registry.NewOptions()),
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency))
	defer stopRuntime(t, r)

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

	r.appChannel = &failingChannel
	r.runtimeConfig.appConnectionConfig.Protocol = protocol.HTTPProtocol

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

	r.runtimeConfig.registry.Bindings().RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &failingBinding
		},
		"failingoutput",
	)

	output := componentsV1alpha1.Component{}
	output.ObjectMeta.Name = "failOutput"
	output.Spec.Type = "bindings.failingoutput"
	err := r.processor.One(context.TODO(), output)
	assert.NoError(t, err)

	t.Run("output binding retries on failure with resiliency", func(t *testing.T) {
		req := &bindings.InvokeRequest{
			Data:      []byte("outputFailingKey"),
			Operation: "create",
		}
		_, err := r.sendToOutputBinding("failOutput", req)

		assert.NoError(t, err)
		assert.Equal(t, 2, failingBinding.Failure.CallCount("outputFailingKey"))
	})

	t.Run("output binding times out with resiliency", func(t *testing.T) {
		req := &bindings.InvokeRequest{
			Data:      []byte("outputTimeoutKey"),
			Operation: "create",
		}
		start := time.Now()
		_, err := r.sendToOutputBinding("failOutput", req)
		end := time.Now()

		assert.NotNil(t, err)
		assert.Equal(t, 2, failingBinding.Failure.CallCount("outputTimeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("input binding retries on failure with resiliency", func(t *testing.T) {
		_, err := r.sendBindingEventToApp("failingInputBinding", []byte("inputFailingKey"), map[string]string{})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount("inputFailingKey"))
	})

	t.Run("input binding times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := r.sendBindingEventToApp("failingInputBinding", []byte("inputTimeoutKey"), map[string]string{})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount("inputTimeoutKey"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

func TestActorReentrancyConfig(t *testing.T) {
	fullConfig := `{
		"entities":["actorType1", "actorType2"],
		"actorIdleTimeout": "1h",
		"actorScanInterval": "30s",
		"drainOngoingCallTimeout": "30s",
		"drainRebalancedActors": true,
		"reentrancy": {
		  "enabled": true,
		  "maxStackDepth": 64
		}
	  }`
	limit := 64

	minimumConfig := `{
		"entities":["actorType1", "actorType2"],
		"actorIdleTimeout": "1h",
		"actorScanInterval": "30s",
		"drainOngoingCallTimeout": "30s",
		"drainRebalancedActors": true,
		"reentrancy": {
		  "enabled": true
		}
	  }`

	emptyConfig := `{
		"entities":["actorType1", "actorType2"],
		"actorIdleTimeout": "1h",
		"actorScanInterval": "30s",
		"drainOngoingCallTimeout": "30s",
		"drainRebalancedActors": true
	  }`

	testcases := []struct {
		Name               string
		Config             []byte
		ExpectedReentrancy bool
		ExpectedLimit      *int
	}{
		{
			Name:               "Test full configuration",
			Config:             []byte(fullConfig),
			ExpectedReentrancy: true,
			ExpectedLimit:      &limit,
		},
		{
			Name:               "Test minimum configuration",
			Config:             []byte(minimumConfig),
			ExpectedReentrancy: true,
			ExpectedLimit:      nil,
		},
		{
			Name:               "Test minimum configuration",
			Config:             []byte(emptyConfig),
			ExpectedReentrancy: false,
			ExpectedLimit:      nil,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			r := newDaprRuntime(&internalConfig{
				mode:     modes.KubernetesMode,
				registry: registry.New(registry.NewOptions()),
			}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

			mockAppChannel := new(channelt.MockAppChannel)
			r.appChannel = mockAppChannel
			r.runtimeConfig.appConnectionConfig.Protocol = protocol.HTTPProtocol

			configResp := config.ApplicationConfig{}
			json.Unmarshal(tc.Config, &configResp)

			mockAppChannel.On("GetAppConfig").Return(&configResp, nil)

			r.loadAppConfiguration()

			assert.NotNil(t, r.appConfig)

			assert.Equal(t, tc.ExpectedReentrancy, r.appConfig.Reentrancy.Enabled)
			assert.Equal(t, tc.ExpectedLimit, r.appConfig.Reentrancy.MaxStackDepth)
		})
	}
}

type mockPubSub struct {
	pubsub.PubSub
	closeErr error
}

func (p *mockPubSub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	return nil
}

func (p *mockPubSub) Close() error {
	return p.closeErr
}

type mockStateStore struct {
	state.Store
	closeErr error
}

func (s *mockStateStore) Init(ctx context.Context, metadata state.Metadata) error {
	return nil
}

func (s *mockStateStore) Close() error {
	return s.closeErr
}

type mockNameResolver struct {
	nameresolution.Resolver
	closeErr error
}

func (n *mockNameResolver) Init(metadata nameresolution.Metadata) error {
	return nil
}

func (n *mockNameResolver) Close() error {
	return n.closeErr
}

func TestStopWithErrors(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	testErr := errors.New("mock close error")

	rt.runtimeConfig.registry.Bindings().RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &mockBinding{closeErr: testErr}
		},
		"output",
	)
	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return &mockPubSub{closeErr: testErr}
		},
		"pubsub",
	)
	rt.runtimeConfig.registry.StateStores().RegisterComponent(
		func(_ logger.Logger) state.Store {
			return &mockStateStore{closeErr: testErr}
		},
		"statestore",
	)
	rt.runtimeConfig.registry.SecretStores().RegisterComponent(
		func(_ logger.Logger) secretstores.SecretStore {
			return &rtmock.SecretStore{CloseErr: testErr}
		},
		"secretstore",
	)

	mockOutputBindingComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "bindings.output",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "output",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockPubSubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "pubsub.pubsub",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "pubsub",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockStateComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "state.statestore",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "statestore",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockSecretsComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "secretstores.secretstore",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "secretstore",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}

	require.NoError(t, rt.processor.One(context.TODO(), mockOutputBindingComponent))
	require.NoError(t, rt.processor.One(context.TODO(), mockPubSubComponent))
	require.NoError(t, rt.processor.One(context.TODO(), mockStateComponent))
	require.NoError(t, rt.processor.One(context.TODO(), mockSecretsComponent))
	rt.nameResolver = &mockNameResolver{closeErr: testErr}

	err := rt.shutdownOutputComponents()
	require.Error(t, err)
	assert.Len(t, strings.Split(err.Error(), "\n"), 5)
}

func stopRuntime(t *testing.T, rt *DaprRuntime) {
	rt.stopActor()
	assert.NoError(t, rt.shutdownOutputComponents())
	time.Sleep(100 * time.Millisecond)
}

func TestFindMatchingRoute(t *testing.T) {
	r, err := createRoutingRule(`event.type == "MyEventType"`, "mypath")
	require.NoError(t, err)
	rules := []*runtimePubsub.Rule{r}
	path, shouldProcess, err := findMatchingRoute(rules, map[string]interface{}{
		"type": "MyEventType",
	})
	require.NoError(t, err)
	assert.Equal(t, "mypath", path)
	assert.True(t, shouldProcess)
}

func createRoutingRule(match, path string) (*runtimePubsub.Rule, error) {
	var e *expr.Expr
	matchTrimmed := strings.TrimSpace(match)
	if matchTrimmed != "" {
		e = &expr.Expr{}
		if err := e.DecodeString(matchTrimmed); err != nil {
			return nil, err
		}
	}

	return &runtimePubsub.Rule{
		Match: e,
		Path:  path,
	}, nil
}

func TestGetAppHTTPChannelConfigWithCustomChannel(t *testing.T) {
	rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, "http", 0)
	rt.runtimeConfig.appConnectionConfig.ChannelAddress = "my.app"

	defer stopRuntime(t, rt)

	p, err := rt.buildAppHTTPPipeline()
	assert.Nil(t, err)

	c := rt.getAppHTTPChannelConfig(p)
	assert.Equal(t, "http://my.app:0", c.Endpoint)
}

func TestComponentsCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, _ := strconv.Atoi(u.Port())

	c := make(chan struct{})
	callbackInvoked := false

	cfg := NewTestDaprRuntimeConfig(modes.StandaloneMode, "http", port)
	rt := newDaprRuntime(cfg, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	rt.runtimeConfig.registry = registry.New(registry.NewOptions().WithComponentsCallback(func(components registry.ComponentRegistry) error {
		close(c)
		callbackInvoked = true
		return nil
	}))
	defer stopRuntime(t, rt)

	assert.NoError(t, rt.Run(context.TODO()))
	defer rt.Shutdown(0)

	select {
	case <-c:
	case <-time.After(10 * time.Second):
	}

	assert.True(t, callbackInvoked, "component callback was not invoked")
}

func TestGRPCProxy(t *testing.T) {
	// setup gRPC server
	serverPort, _ := freeport.GetFreePort()
	teardown, err := runGRPCApp(serverPort)
	require.NoError(t, err)
	defer teardown()

	// setup proxy
	rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, "grpc", serverPort)
	internalPort, _ := freeport.GetFreePort()
	rt.runtimeConfig.internalGRPCPort = internalPort
	defer stopRuntime(t, rt)

	rt.runtimeConfig.registry.NameResolutions().RegisterComponent(
		func(_ logger.Logger) nameresolution.Resolver {
			mockResolver := new(daprt.MockResolver)
			// proxy to server anytime
			mockResolver.On("Init", mock.Anything).Return(nil)
			mockResolver.On("ResolveID", mock.Anything).Return(fmt.Sprintf("localhost:%d", serverPort), nil)
			return mockResolver
		},
		"mdns", // for standalone mode
	)

	go func() {
		rt.Run(context.TODO())
	}()
	defer rt.Shutdown(0)

	time.Sleep(time.Second)

	req := &pb.PingRequest{Value: "foo"}

	t.Run("proxy single streaming request", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.TODO(), time.Second)
		defer cancel()
		stream, err := pingStreamClient(ctx, internalPort)
		require.NoError(t, err)

		require.NoError(t, stream.Send(req), "sending to PingStream must not fail")
		resp, err := stream.Recv()
		require.NoError(t, err)
		require.NotNil(t, resp, "resp must not be nil")

		require.NoError(t, stream.CloseSend(), "no error on close send")
	})

	t.Run("proxy concurrent streaming requests", func(t *testing.T) {
		ctx1, cancel := context.WithTimeout(context.TODO(), time.Second)
		defer cancel()
		stream1, err := pingStreamClient(ctx1, internalPort)
		require.NoError(t, err)

		ctx2, cancel := context.WithTimeout(context.TODO(), time.Second)
		defer cancel()
		stream2, err := pingStreamClient(ctx2, internalPort)
		require.NoError(t, err)

		require.NoError(t, stream1.Send(req), "sending to PingStream must not fail")
		resp, err := stream1.Recv()
		require.NoError(t, err)
		require.NotNil(t, resp, "resp must not be nil")

		require.NoError(t, stream2.Send(req), "sending to PingStream must not fail")
		resp, err = stream2.Recv()
		require.NoError(t, err)
		require.NotNil(t, resp, "resp must not be nil")

		require.NoError(t, stream1.CloseSend(), "no error on close send")
		require.NoError(t, stream2.CloseSend(), "no error on close send")
	})
}

func TestGetComponentsCapabilitiesMap(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	mockStateStore := new(daprt.MockStateStore)
	rt.runtimeConfig.registry.StateStores().RegisterComponent(
		func(_ logger.Logger) state.Store {
			return mockStateStore
		},
		"mockState",
	)
	mockStateStore.On("Init", mock.Anything).Return(nil)
	cStateStore := componentsV1alpha1.Component{}
	cStateStore.ObjectMeta.Name = "testStateStoreName"
	cStateStore.Spec.Type = "state.mockState"

	mockPubSub := new(daprt.MockPubSub)
	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)
	mockPubSub.On("Features").Return([]pubsub.Feature{pubsub.FeatureMessageTTL, pubsub.FeatureSubscribeWildcards})
	cPubSub := componentsV1alpha1.Component{}
	cPubSub.ObjectMeta.Name = "mockPubSub"
	cPubSub.Spec.Type = "pubsub.mockPubSub"

	rt.runtimeConfig.registry.Bindings().RegisterInputBinding(
		func(_ logger.Logger) bindings.InputBinding {
			return &daprt.MockBinding{}
		},
		"testInputBinding",
	)
	cin := componentsV1alpha1.Component{}
	cin.ObjectMeta.Name = "testInputBinding"
	cin.Spec.Type = "bindings.testInputBinding"

	rt.runtimeConfig.registry.Bindings().RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &daprt.MockBinding{}
		},
		"testOutputBinding",
	)
	cout := componentsV1alpha1.Component{}
	cout.ObjectMeta.Name = "testOutputBinding"
	cout.Spec.Type = "bindings.testOutputBinding"

	mockSecretStoreName := "mockSecretStore"
	mockSecretStore := new(daprt.FakeSecretStore)
	rt.runtimeConfig.registry.SecretStores().RegisterComponent(
		func(_ logger.Logger) secretstores.SecretStore {
			return mockSecretStore
		},
		mockSecretStoreName,
	)
	cSecretStore := componentsV1alpha1.Component{}
	cSecretStore.ObjectMeta.Name = mockSecretStoreName
	cSecretStore.Spec.Type = "secretstores.mockSecretStore"

	require.NoError(t, rt.processor.One(context.TODO(), cin))
	require.NoError(t, rt.processor.One(context.TODO(), cout))
	require.NoError(t, rt.processor.One(context.TODO(), cPubSub))
	require.NoError(t, rt.processor.One(context.TODO(), cStateStore))
	require.NoError(t, rt.processor.One(context.TODO(), cSecretStore))

	capabilities := rt.getComponentsCapabilitesMap()
	assert.Equal(t, 5, len(capabilities),
		"All 5 registered components have are present in capabilities (stateStore pubSub input output secretStore)")
	assert.Equal(t, 2, len(capabilities["mockPubSub"]),
		"mockPubSub has 2 features because we mocked it so")
	assert.Equal(t, 1, len(capabilities["testInputBinding"]),
		"Input bindings always have INPUT_BINDING added to their capabilities")
	assert.Equal(t, 1, len(capabilities["testOutputBinding"]),
		"Output bindings always have OUTPUT_BINDING added to their capabilities")
	assert.Equal(t, 1, len(capabilities[mockSecretStoreName]),
		"mockSecretStore has a single feature and it should be present")
}

func runGRPCApp(port int) (func(), error) {
	serverListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return func() {}, err
	}

	server := grpc.NewServer()
	pb.RegisterTestServiceServer(server, &pingStreamService{})
	go func() {
		server.Serve(serverListener)
	}()
	teardown := func() {
		server.Stop()
	}

	return teardown, nil
}

func pingStreamClient(ctx context.Context, port int) (pb.TestService_PingStreamClient, error) {
	clientConn, err := grpc.DialContext(
		ctx,
		fmt.Sprintf("localhost:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	testClient := pb.NewTestServiceClient(clientConn)

	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", "dummy")
	return testClient.PingStream(ctx)
}

type pingStreamService struct {
	pb.TestServiceServer
}

func (s *pingStreamService) PingStream(stream pb.TestService_PingStreamServer) error {
	counter := int32(0)
	for {
		ping, err := stream.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		pong := &pb.PingResponse{Value: ping.Value, Counter: counter}
		if err := stream.Send(pong); err != nil {
			return err
		}
		counter++
	}
	return nil
}

func matchContextInterface(v any) bool {
	_, ok := v.(context.Context)
	return ok
}

func matchDaprRequestMethod(method string) any {
	return mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
		if req == nil || req.Message() == nil || req.Message().Method != method {
			return false
		}
		return true
	})
}

func TestNamespacedPublisher(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.namespace = "ns1"
	defer stopRuntime(t, rt)

	for name := range rt.compStore.ListPubSubs() {
		rt.compStore.DeletePubSub(name)
	}
	rt.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
		Component:       &mockPublishPubSub{},
		NamespaceScoped: true,
	})
	rt.Publish(&pubsub.PublishRequest{
		PubsubName: TestPubsubName,
		Topic:      "topic0",
	})

	pubSub, ok := rt.compStore.GetPubSub(TestPubsubName)
	require.True(t, ok)
	assert.Equal(t, "ns1topic0", pubSub.Component.(*mockPublishPubSub).PublishedRequest.Load().Topic)
}

func TestGracefulShutdownPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	mockPubSub := new(daprt.MockPubSub)
	rt.runtimeConfig.registry.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)
	rt.runtimeConfig.gracefulShutdownDuration = 5 * time.Second
	mockPubSub.On("Init", mock.Anything).Return(nil)
	mockPubSub.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub.On("Close").Return(nil)

	cPubSub := componentsV1alpha1.Component{}
	cPubSub.ObjectMeta.Name = "mockPubSub"
	cPubSub.Spec.Type = "pubsub.mockPubSub"

	subscriptionItems := []runtimePubsub.SubscriptionJSON{
		{PubsubName: "mockPubSub", Topic: "topic0", Route: "shutdown"},
	}
	sub, _ := json.Marshal(subscriptionItems)
	fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
	fakeResp.WithRawDataBytes(sub).
		WithContentType("application/json")
	defer fakeResp.Close()

	mockAppChannel := new(channelt.MockAppChannel)
	rt.appChannel = mockAppChannel
	mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
	require.NoError(t, rt.processor.One(context.TODO(), cPubSub))
	mockPubSub.AssertCalled(t, "Init", mock.Anything)
	rt.startSubscriptions()
	mockPubSub.AssertCalled(t, "Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler"))
	assert.NoError(t, rt.pubsubCtx.Err())
	rt.running.Store(true)
	go sendSigterm(rt)
	select {
	case <-rt.pubsubCtx.Done():
		assert.Error(t, rt.pubsubCtx.Err(), context.Canceled)
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "pubsub shutdown timed out")
	}
}

func TestGracefulShutdownBindings(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.runtimeConfig.gracefulShutdownDuration = 3 * time.Second
	rt.runtimeConfig.registry.Bindings().RegisterInputBinding(
		func(_ logger.Logger) bindings.InputBinding {
			return &daprt.MockBinding{}
		},
		"testInputBinding",
	)
	cin := componentsV1alpha1.Component{}
	cin.ObjectMeta.Name = "testInputBinding"
	cin.Spec.Type = "bindings.testInputBinding"

	rt.runtimeConfig.registry.Bindings().RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &daprt.MockBinding{}
		},
		"testOutputBinding",
	)
	cout := componentsV1alpha1.Component{}
	cout.ObjectMeta.Name = "testOutputBinding"
	cout.Spec.Type = "bindings.testOutputBinding"
	require.NoError(t, rt.processor.One(context.TODO(), cin))
	require.NoError(t, rt.processor.One(context.TODO(), cout))
	assert.Equal(t, len(rt.compStore.ListInputBindings()), 1)
	assert.Equal(t, len(rt.compStore.ListOutputBindings()), 1)
	rt.running.Store(true)
	rt.inputBindingsCtx, rt.inputBindingsCancel = context.WithCancel(rt.ctx)
	go sendSigterm(rt)
	select {
	case <-rt.inputBindingsCtx.Done():
		return
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "input bindings shutdown timed out")
	}
}

func TestGracefulShutdownActors(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.runtimeConfig.gracefulShutdownDuration = 5 * time.Second

	bytes := make([]byte, 32)
	rand.Read(bytes)
	encryptKey := hex.EncodeToString(bytes)

	mockStateComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:    "state.mockState",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{
				{
					Name: "ACTORSTATESTORE",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{Raw: []byte("true")},
					},
				},
				{
					Name: "primaryEncryptionKey",
					Value: commonapi.DynamicValue{
						JSON: v1.JSON{Raw: []byte(encryptKey)},
					},
				},
			},
		},
		Auth: componentsV1alpha1.Auth{
			SecretStore: "mockSecretStore",
		},
	}

	// setup
	initMockStateStoreForRuntime(rt, encryptKey, nil)

	// act
	err := rt.processor.One(context.TODO(), mockStateComponent)

	// assert
	assert.NoError(t, err, "expected no error")

	rt.namespace = "test"
	rt.runtimeConfig.mTLSEnabled = true
	assert.Nil(t, rt.initActors())

	rt.running.Store(true)
	go sendSigterm(rt)
	<-time.After(rt.runtimeConfig.gracefulShutdownDuration + 3*time.Second)

	var activeActCount int32
	activeActors := rt.actor.GetActiveActorsCount(rt.ctx)
	for _, v := range activeActors {
		activeActCount += v.Count
	}
	assert.Equal(t, activeActCount, int32(0))
}

func initMockStateStoreForRuntime(rt *DaprRuntime, encryptKey string, e error) *daprt.MockStateStore {
	mockStateStore := new(daprt.MockStateStore)

	rt.runtimeConfig.registry.StateStores().RegisterComponent(
		func(_ logger.Logger) state.Store {
			return mockStateStore
		},
		"mockState",
	)

	expectedMetadata := state.Metadata{Base: mdata.Base{
		Name: TestPubsubName,
		Properties: map[string]string{
			"actorstatestore":      "true",
			"primaryEncryptionKey": encryptKey,
		},
	}}
	expectedMetadataUppercase := state.Metadata{Base: mdata.Base{
		Name: TestPubsubName,
		Properties: map[string]string{
			"ACTORSTATESTORE":      "true",
			"primaryEncryptionKey": encryptKey,
		},
	}}

	mockStateStore.On("Init", expectedMetadata).Return(e)
	mockStateStore.On("Init", expectedMetadataUppercase).Return(e)

	return mockStateStore
}

func TestTraceShutdown(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.runtimeConfig.gracefulShutdownDuration = 5 * time.Second
	rt.globalConfig.Spec.TracingSpec = &config.TracingSpec{
		Otel: &config.OtelSpec{
			EndpointAddress: "foo.bar",
			IsSecure:        ptr.Of(false),
			Protocol:        "http",
		},
	}
	rt.hostAddress = "localhost:3000"
	tpStore := newOpentelemetryTracerProviderStore()
	require.NoError(t, rt.setupTracing(rt.hostAddress, tpStore))
	assert.NotNil(t, rt.tracerProvider)

	rt.running.Store(true)
	go sendSigterm(rt)
	<-rt.ctx.Done()
	assert.Nil(t, rt.tracerProvider)
}

func sendSigterm(rt *DaprRuntime) {
	rt.Shutdown(rt.runtimeConfig.gracefulShutdownDuration)
}

func createTestEndpoint(name, baseURL string) httpEndpointV1alpha1.HTTPEndpoint {
	return httpEndpointV1alpha1.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: httpEndpointV1alpha1.HTTPEndpointSpec{
			BaseURL: baseURL,
		},
	}
}

func TestHTTPEndpointsUpdate(t *testing.T) {
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	defer stopRuntime(t, rt)

	mockOpCli := newMockOperatorClient()
	rt.operatorClient = mockOpCli

	processedCh := make(chan struct{}, 1)
	mockProcessHTTPEndpoints := func() {
		for endpoint := range rt.pendingHTTPEndpoints {
			if endpoint.Name == "" {
				continue
			}
			rt.compStore.AddHTTPEndpoint(endpoint)
			processedCh <- struct{}{}
		}
	}
	go mockProcessHTTPEndpoints()
	go rt.beginHTTPEndpointsUpdates()

	endpoint1 := createTestEndpoint("mockEndpoint1", "http://testurl.com")
	endpoint2 := createTestEndpoint("mockEndpoint2", "http://testurl2.com")
	endpoint3 := createTestEndpoint("mockEndpoint3", "http://testurl3.com")

	// Allow a new stream to create.
	mockOpCli.AllowOneNewClientEndpointStreamCreate()

	// Wait a new stream created.
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	if err := mockOpCli.WaitOneNewClientHTTPEndpointStreamCreated(waitCtx); err != nil {
		t.Errorf("Wait new stream err: %s", err.Error())
		t.FailNow()
	}

	// Wait endpoint1 received and processed.
	mockOpCli.UpdateHTTPEndpoint(&endpoint1)
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect endpoint [endpoint1] processed.")
		t.FailNow()
	}
	_, exists := rt.compStore.GetHTTPEndpoint(endpoint1.Name)
	assert.True(t, exists, fmt.Sprintf("expect http endpoint with name: %s", endpoint1.Name))

	// Close all client streams to trigger a stream error in `beginHTTPEndpointsUpdates`
	mockOpCli.CloseAllClientHTTPEndpointStreams()

	// Update during stream error.
	mockOpCli.UpdateHTTPEndpoint(&endpoint2)

	// Assert no client stream created.
	assert.Equal(t, mockOpCli.ClientHTTPEndpointStreamCount(), 0, "Expect 0 client stream")

	// Allow a new stream to create.
	mockOpCli.AllowOneNewClientEndpointStreamCreate()
	// Wait a new stream created.
	waitCtx, cancel = context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	// was failing here
	if err := mockOpCli.WaitOneNewClientHTTPEndpointStreamCreated(waitCtx); err != nil {
		t.Errorf("Wait new stream err: %s", err.Error())
		t.FailNow()
	}

	// Wait endpoint2 received and processed.
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect http endpoint [endpoint2] processed.")
		t.FailNow()
	}
	_, exists = rt.compStore.GetHTTPEndpoint(endpoint2.Name)
	assert.True(t, exists, fmt.Sprintf("expect http endpoint with name: %s", endpoint2.Name))

	mockOpCli.UpdateHTTPEndpoint(&endpoint3)

	// Wait endpoint3 received and processed.
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect endpoint [endpoint3] processed.")
		t.FailNow()
	}
	_, exists = rt.compStore.GetHTTPEndpoint(endpoint3.Name)
	assert.True(t, exists, fmt.Sprintf("expect http endpoint with name: %s", endpoint3.Name))
}

type MockKubernetesStateStore struct {
	callback func()
}

func (m *MockKubernetesStateStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	if m.callback != nil {
		m.callback()
	}
	return nil
}

func (m *MockKubernetesStateStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (m *MockKubernetesStateStore) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	response := map[string]map[string]string{}
	response["k8s-secret"] = map[string]string{
		"key1":   "value1",
		"_value": "_value_data",
		"name1":  "value1",
	}
	return secretstores.BulkGetSecretResponse{
		Data: response,
	}, nil
}

func (m *MockKubernetesStateStore) Close() error {
	return nil
}

func (m *MockKubernetesStateStore) Features() []secretstores.Feature {
	return []secretstores.Feature{}
}

func (m MockKubernetesStateStore) GetComponentMetadata() map[string]string {
	return nil
}

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func NewMockKubernetesStoreWithInitCallback(cb func()) secretstores.SecretStore {
	return &MockKubernetesStateStore{callback: cb}
}

func TestIsEnvVarAllowed(t *testing.T) {
	t.Run("no allowlist", func(t *testing.T) {
		tests := []struct {
			name string
			key  string
			want bool
		}{
			{name: "empty string is not allowed", key: "", want: false},
			{name: "key is allowed", key: "FOO", want: true},
			{name: "keys starting with DAPR_ are denied", key: "DAPR_TEST", want: false},
			{name: "APP_API_TOKEN is denied", key: "APP_API_TOKEN", want: false},
			{name: "keys with a space are denied", key: "FOO BAR", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := isEnvVarAllowed(tt.key); got != tt.want {
					t.Errorf("isEnvVarAllowed(%q) = %v, want %v", tt.key, got, tt.want)
				}
			})
		}
	})

	t.Run("with allowlist", func(t *testing.T) {
		t.Setenv(authConsts.EnvKeysEnvVar, "FOO BAR TEST")

		tests := []struct {
			name string
			key  string
			want bool
		}{
			{name: "FOO is allowed", key: "FOO", want: true},
			{name: "BAR is allowed", key: "BAR", want: true},
			{name: "TEST is allowed", key: "TEST", want: true},
			{name: "FO is not allowed", key: "FO", want: false},
			{name: "EST is not allowed", key: "EST", want: false},
			{name: "BA is not allowed", key: "BA", want: false},
			{name: "AR is not allowed", key: "AR", want: false},
			{name: "keys starting with DAPR_ are denied", key: "DAPR_TEST", want: false},
			{name: "APP_API_TOKEN is denied", key: "APP_API_TOKEN", want: false},
			{name: "keys with a space are denied", key: "FOO BAR", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := isEnvVarAllowed(tt.key); got != tt.want {
					t.Errorf("isEnvVarAllowed(%q) = %v, want %v", tt.key, got, tt.want)
				}
			})
		}
	})
}

// helper to populate subscription array for 2 pubsubs.
// 'topics' are the topics for the first pubsub.
// 'topics2' are the topics for the second pubsub.
func getSubscriptionsJSONString(topics []string, topics2 []string) string {
	s := []runtimePubsub.SubscriptionJSON{}
	for _, t := range topics {
		s = append(s, runtimePubsub.SubscriptionJSON{
			PubsubName: TestPubsubName,
			Topic:      t,
			Routes: runtimePubsub.RoutesJSON{
				Default: t,
			},
		})
	}

	for _, t := range topics2 {
		s = append(s, runtimePubsub.SubscriptionJSON{
			PubsubName: TestSecondPubsubName,
			Topic:      t,
			Routes: runtimePubsub.RoutesJSON{
				Default: t,
			},
		})
	}
	b, _ := json.Marshal(&s)

	return string(b)
}

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
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		mockAppChannel := new(channelt.MockAppChannel)

		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		rt.appChannel = mockAppChannel
		defer stopRuntime(t, rt)

		m := &mockBinding{}

		rt.compStore.AddInputBinding("test", m)
		err := rt.startReadingFromBindings()

		assert.NoError(t, err)
		assert.True(t, mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything))
	})

	t.Run("No OPTIONS request when direction is specified", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		mockAppChannel := new(channelt.MockAppChannel)

		mockAppChannel.On("InvokeMethod", mock.Anything, mock.Anything).Return(invokev1.NewInvokeMethodResponse(200, "OK", nil), nil)
		rt.appChannel = mockAppChannel
		defer stopRuntime(t, rt)

		m := &mockBinding{
			metadata: map[string]string{
				"direction": "input",
			},
		}

		rt.compStore.AddInputBinding("test", m)
		rt.compStore.AddComponent(componentsV1alpha1.Component{
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
		})
		err := rt.startReadingFromBindings()

		assert.NoError(t, err)
		assert.True(t, mockAppChannel.AssertNotCalled(t, "InvokeMethod", mock.Anything, mock.Anything))
	})
}

func TestGetHTTPEndpointAppChannel(t *testing.T) {
	t.Run("no TLS channel", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		conf, err := rt.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpEndpointV1alpha1.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpEndpointV1alpha1.HTTPEndpointSpec{},
		})

		assert.NoError(t, err)
		assert.Nil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel with Root CA", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		conf, err := rt.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpEndpointV1alpha1.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpEndpointV1alpha1.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.NoError(t, err)
		assert.NotNil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel without Root CA", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		conf, err := rt.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpEndpointV1alpha1.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpEndpointV1alpha1.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.NoError(t, err)
		assert.NotNil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel with invalid Root CA", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		_, err := rt.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpEndpointV1alpha1.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpEndpointV1alpha1.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("asdsdsdassjkdctewzxabcdef")},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.Error(t, err)
	})
}
