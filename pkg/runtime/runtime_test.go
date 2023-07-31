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
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/lock"
	mdata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
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
	pb "github.com/dapr/dapr/pkg/grpc/proxy/testservice"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/processor"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

const (
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	TestLockName         = "testlock"
	resourcesDir         = "./components"
	maxGRPCServerUptime  = 200 * time.Millisecond
)

var testCertRoot = `-----BEGIN CERTIFICATE-----
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

func TestNewRuntime(t *testing.T) {
	// act
	r, err := newDaprRuntime(context.Background(), &internalConfig{
		registry: registry.New(registry.NewOptions()),
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

	// assert
	assert.NoError(t, err)
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestProcessComponentsAndDependents(t *testing.T) {
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
	defer stopRuntime(t, rt)

	incorrectComponentType := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsubs.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	t.Run("test incorrect type", func(t *testing.T) {
		err := rt.processComponentAndDependents(context.Background(), incorrectComponentType)
		assert.Error(t, err, "expected an error")
		assert.Equal(t, "incorrect type pubsubs.mockPubSub", err.Error(), "expected error strings to match")
	})
}

func TestDoProcessComponent(t *testing.T) {
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
	defer stopRuntime(t, rt)

	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
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
		err := rt.processor.Init(context.Background(), lockComponent)

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
		err := rt.processor.Init(context.Background(), lockComponentV3)

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
		err := rt.processor.Init(context.Background(), lockComponentWithWrongStrategy)
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
		err := rt.processor.Init(context.Background(), lockComponent)
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
				Properties: daprt.GetFakeProperties(),
			},
		}

		mockPubSub.On("Init", expectedMetadata).Return(assert.AnError)

		// act
		err := rt.processor.Init(context.Background(), pubsubComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "testpubsub (pubsub.mockPubSub/v1)", assert.AnError).Error(), "expected error strings to match")
	})

	t.Run("test invalid category component", func(t *testing.T) {
		// act
		err := rt.processor.Init(context.Background(), componentsV1alpha1.Component{
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
	rt, err := NewTestDaprRuntime(modes.KubernetesMode)
	require.NoError(t, err)
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

	go rt.beginComponentsUpdates(context.Background())

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

func TestInitSecretStores(t *testing.T) {
	t.Run("init with store", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err = rt.processComponentAndDependents(context.Background(), componentsV1alpha1.Component{
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err = rt.processComponentAndDependents(context.Background(), componentsV1alpha1.Component{
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		rt.processComponentAndDependents(context.Background(), componentsV1alpha1.Component{
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "targetResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "anotherResolver", nil)

		// act
		err = rt.initNameResolution()

		// assert
		assert.Error(t, err)
	})

	t.Run("test init nameresolution", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "someResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "someResolver", nil)

		// act
		err = rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in StandaloneMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err = rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in StandaloneMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err = rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in KubernetesMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(modes.KubernetesMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err = rt.initNameResolution()

		// assert
		assert.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in KubernetesMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(modes.KubernetesMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err = rt.initNameResolution()

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
			rt, err := NewTestDaprRuntime(modes.StandaloneMode)
			require.NoError(t, err)
			defer stopRuntime(t, rt)
			rt.globalConfig.Spec.TracingSpec = &tc.tracingConfig
			if tc.hostAddress != "" {
				rt.hostAddress = tc.hostAddress
			}
			// Setup tracing with the fake tracer provider  store to confirm
			// the right exporter was registered.
			tpStore := newFakeTracerProviderStore()
			if err := rt.setupTracing(context.Background(), rt.hostAddress, tpStore); tc.expectedErr != "" {
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
				rt.setupTracing(context.Background(), rt.hostAddress, newOpentelemetryTracerProviderStore())
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
			Metadata: daprt.GetFakeMetadataItems(),
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
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
		var uuid0, uuid1, uuid2 uuid.UUID
		uuid0, err = uuid.Parse(consumerID)
		assert.NoError(t, err)

		twoUUIDs := metadata.Properties["twoUUIDs"]
		uuids := strings.Split(twoUUIDs, " ")
		assert.Equal(t, 2, len(uuids))
		uuid1, err = uuid.Parse(uuids[0])
		assert.NoError(t, err)
		uuid2, err = uuid.Parse(uuids[1])
		assert.NoError(t, err)

		assert.NotEqual(t, uuid0, uuid1)
		assert.NotEqual(t, uuid0, uuid2)
		assert.NotEqual(t, uuid1, uuid2)
	})

	err = rt.processComponentAndDependents(context.Background(), pubsubComponent)
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
			Metadata: daprt.GetFakeMetadataItems(),
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
	rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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

	err := rt.processComponentAndDependents(context.Background(), pubsubComponent)
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
			Metadata: daprt.GetFakeMetadataItems(),
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
	rt, _ := NewTestDaprRuntimeWithID(modes.KubernetesMode, "app1")

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

	err := rt.processComponentAndDependents(context.Background(), pubsubComponent)
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
			Metadata: daprt.GetFakeMetadataItems(),
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
	rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
	rt.runtimeConfig.id = daprt.TestRuntimeConfigID
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
			assert.Equal(t, daprt.TestRuntimeConfigID, appID)
		}
	})

	err := rt.processComponentAndDependents(context.Background(), pubsubComponent)
	assert.NoError(t, err)
}

func TestOnComponentUpdated(t *testing.T) {
	t.Run("component spec changed, component is updated", func(t *testing.T) {
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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

		updated := rt.onComponentUpdated(context.Background(), componentsV1alpha1.Component{
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
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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

		updated := rt.onComponentUpdated(context.Background(), componentsV1alpha1.Component{
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

func TestPopulateSecretsConfiguration(t *testing.T) {
	t.Run("secret store configuration is populated", func(t *testing.T) {
		// setup
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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

		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		m := NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			secretstoresLoader.BuiltinKubernetesSecretStore,
		)

		// add Kubernetes component manually
		rt.processComponentAndDependents(context.Background(), componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: secretstoresLoader.BuiltinKubernetesSecretStore,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetes",
				Version: "v1",
			},
		})

		updated, unready := rt.processResourceSecrets(context.Background(), mockBinding)
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

		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return &rtmock.SecretStore{}
			},
			"mock",
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.processComponentAndDependents(context.Background(), componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.mock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)

		updated, unready := rt.processResourceSecrets(context.Background(), mockBinding)
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

		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)

		updated, unready := rt.processResourceSecrets(context.Background(), mockBinding)
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

		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)

		updated, unready := rt.processResourceSecrets(context.Background(), mockBinding)
		assert.True(t, updated)
		assert.Equal(t, "", mockBinding.Spec.Metadata[0].Value.String())
		assert.Equal(t, "", mockBinding.Spec.Metadata[1].Value.String())
		assert.Empty(t, unready)
	})
}

// Test that flushOutstandingComponents waits for components.
func TestFlushOutstandingComponent(t *testing.T) {
	t.Run("We can call flushOustandingComponents more than once", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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

		go rt.processComponents(context.Background())
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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

		go rt.processComponents(context.Background())
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
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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
			err := rt.processComponentAndDependents(context.Background(), comp)
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

func NewTestDaprRuntime(mode modes.DaprMode) (*DaprRuntime, error) {
	return NewTestDaprRuntimeWithProtocol(mode, string(protocol.HTTPProtocol), 1024)
}

func NewTestDaprRuntimeWithID(mode modes.DaprMode, id string) (*DaprRuntime, error) {
	testRuntimeConfig := NewTestDaprRuntimeConfig(modes.StandaloneMode, string(protocol.HTTPProtocol), 1024)
	testRuntimeConfig.id = id
	rt, err := newDaprRuntime(context.Background(), testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	if err != nil {
		return nil, err
	}
	rt.runtimeConfig.mode = mode
	rt.initChannels()
	return rt, nil
}

func NewTestDaprRuntimeWithProtocol(mode modes.DaprMode, protocol string, appPort int) (*DaprRuntime, error) {
	testRuntimeConfig := NewTestDaprRuntimeConfig(modes.StandaloneMode, protocol, appPort)
	rt, err := newDaprRuntime(context.Background(), testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	if err != nil {
		return nil, err
	}
	rt.runtimeConfig.mode = mode
	rt.initChannels()
	return rt, nil
}

func NewTestDaprRuntimeConfig(mode modes.DaprMode, appProtocol string, appPort int) *internalConfig {
	return &internalConfig{
		id:                 daprt.TestRuntimeConfigID,
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
	r, err := NewTestDaprRuntime(modes.StandaloneMode)
	assert.NoError(t, err)
	assert.Equal(t, time.Second, r.runtimeConfig.gracefulShutdownDuration)
}

func TestMTLS(t *testing.T) {
	t.Run("with mTLS enabled", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)

		err = rt.establishSecurity(rt.runtimeConfig.sentryServiceAddress)
		assert.NoError(t, err)
		assert.Nil(t, rt.authenticator)
	})

	t.Run("mTLS disabled, operator fails without TLS certs", func(t *testing.T) {
		rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		_, err := getOperatorClient(context.Background(), rt.runtimeConfig)
		assert.Error(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := rt.getAuthorizedObjects([]componentsV1alpha1.Component{component}, rt.isObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := NewTestDaprRuntime(modes.StandaloneMode)
		require.NoError(t, err)
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
		rt, err := newDaprRuntime(context.Background(), cfg, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
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
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

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
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

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

func TestInitActors(t *testing.T) {
	t.Run("missing namespace on kubernetes", func(t *testing.T) {
		r, err := NewTestDaprRuntime(modes.KubernetesMode)
		assert.NoError(t, err)
		defer stopRuntime(t, r)
		r.namespace = ""
		r.runtimeConfig.mTLSEnabled = true

		err = r.initActors()
		assert.Error(t, err)
	})

	t.Run("actors hosted = true", func(t *testing.T) {
		r, err := NewTestDaprRuntime(modes.KubernetesMode)
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.appConfig = config.ApplicationConfig{
			Entities: []string{"actor1"},
		}

		hosted := len(r.appConfig.Entities) > 0
		assert.True(t, hosted)
	})

	t.Run("actors hosted = false", func(t *testing.T) {
		r, err := NewTestDaprRuntime(modes.KubernetesMode)
		require.NoError(t, err)
		defer stopRuntime(t, r)

		hosted := len(r.appConfig.Entities) > 0
		assert.False(t, hosted)
	})

	t.Run("placement enable = false", func(t *testing.T) {
		r, err := newDaprRuntime(context.Background(), &internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.initChannels()

		err = r.initActors()
		assert.NotNil(t, err)
	})

	t.Run("the state stores can still be initialized normally", func(t *testing.T) {
		r, err := newDaprRuntime(context.Background(), &internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.initChannels()

		assert.Nil(t, r.actor)
		assert.NotNil(t, r.compStore.ListStateStores())
		assert.Equal(t, 0, r.compStore.StateStoresLen())
	})

	t.Run("the actor store can not be initialized normally", func(t *testing.T) {
		r, err := newDaprRuntime(context.Background(), &internalConfig{
			registry: registry.New(registry.NewOptions()),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.initChannels()

		name, ok := r.processor.State().ActorStateStoreName()
		assert.False(t, ok)
		assert.Equal(t, "", name)
		err = r.initActors()
		assert.NotNil(t, err)
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
			r, err := NewTestDaprRuntime(modes.StandaloneMode)
			require.NoError(t, err)

			mockAppChannel := new(channelt.MockAppChannel)
			r.channels.WithAppChannel(mockAppChannel)
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
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)

	testErr := errors.New("mock close error")

	rt.runtimeConfig.registry.Bindings().RegisterOutputBinding(
		func(_ logger.Logger) bindings.OutputBinding {
			return &rtmock.Binding{CloseErr: testErr}
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

	require.NoError(t, rt.processor.Init(context.Background(), mockOutputBindingComponent))
	require.NoError(t, rt.processor.Init(context.Background(), mockPubSubComponent))
	require.NoError(t, rt.processor.Init(context.Background(), mockStateComponent))
	require.NoError(t, rt.processor.Init(context.Background(), mockSecretsComponent))
	rt.nameResolver = &mockNameResolver{closeErr: testErr}

	err = rt.shutdownOutputComponents(context.Background())
	require.Error(t, err)
	assert.Len(t, strings.Split(err.Error(), "\n"), 5)
}

func stopRuntime(t *testing.T, rt *DaprRuntime) {
	rt.stopActor()
	assert.NoError(t, rt.shutdownOutputComponents(context.Background()))
	time.Sleep(100 * time.Millisecond)
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
	rt, err := newDaprRuntime(context.Background(), cfg, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	require.NoError(t, err)
	rt.runtimeConfig.registry = registry.New(registry.NewOptions().WithComponentsCallback(func(components registry.ComponentRegistry) error {
		close(c)
		callbackInvoked = true
		return nil
	}))
	defer stopRuntime(t, rt)

	assert.NoError(t, rt.Run(context.Background()))
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
	rt, err := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, "grpc", serverPort)
	require.NoError(t, err)
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
		rt.Run(context.Background())
	}()
	defer rt.Shutdown(0)

	time.Sleep(time.Second)

	req := &pb.PingRequest{Value: "foo"}

	t.Run("proxy single streaming request", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
		ctx1, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stream1, err := pingStreamClient(ctx1, internalPort)
		require.NoError(t, err)

		ctx2, cancel := context.WithTimeout(context.Background(), time.Second)
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
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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

	require.NoError(t, rt.processor.Init(context.Background(), cin))
	require.NoError(t, rt.processor.Init(context.Background(), cout))
	require.NoError(t, rt.processor.Init(context.Background(), cPubSub))
	require.NoError(t, rt.processor.Init(context.Background(), cStateStore))
	require.NoError(t, rt.processor.Init(context.Background(), cSecretStore))

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

func matchDaprRequestMethod(method string) any {
	return mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
		if req == nil || req.Message() == nil || req.Message().Method != method {
			return false
		}
		return true
	})
}

func TestGracefulShutdownBindings(t *testing.T) {
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
	require.NoError(t, rt.processor.Init(context.Background(), cin))
	require.NoError(t, rt.processor.Init(context.Background(), cout))
	assert.Equal(t, len(rt.compStore.ListInputBindings()), 1)
	assert.Equal(t, len(rt.compStore.ListOutputBindings()), 1)
	rt.running.Store(true)
	go sendSigterm(rt)
	select {
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "input bindings shutdown timed out")
	case <-rt.shutdownC:
	}
}

func TestGracefulShutdownPubSub(t *testing.T) {
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
	rt.channels.WithAppChannel(mockAppChannel)
	mockAppChannel.On("InvokeMethod", mock.MatchedBy(daprt.MatchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
	// Create new processor with mocked app channel.
	rt.processor = processor.New(processor.Options{
		ID:               rt.runtimeConfig.id,
		IsHTTP:           rt.runtimeConfig.appConnectionConfig.Protocol.IsHTTP(),
		PlacementEnabled: len(rt.runtimeConfig.placementAddresses) > 0,
		Registry:         rt.runtimeConfig.registry,
		ComponentStore:   rt.compStore,
		Meta:             rt.meta,
		GlobalConfig:     rt.globalConfig,
		Resiliency:       rt.resiliency,
		Mode:             rt.runtimeConfig.mode,
		Standalone:       rt.runtimeConfig.standalone,
		GRPC:             rt.grpc,
	})
	rt.processor.SetAppChannel(mockAppChannel)
	require.NoError(t, rt.processor.Init(context.Background(), cPubSub))
	mockPubSub.AssertCalled(t, "Init", mock.Anything)
	rt.processor.PubSub().StartSubscriptions(context.Background())
	mockPubSub.AssertCalled(t, "Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler"))
	rt.running.Store(true)
	go sendSigterm(rt)
	select {
	case <-rt.shutdownC:
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "pubsub shutdown timed out")
	}
}

func TestGracefulShutdownActors(t *testing.T) {
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
	err = rt.processor.Init(context.Background(), mockStateComponent)

	// assert
	assert.NoError(t, err, "expected no error")

	rt.namespace = "test"
	rt.runtimeConfig.mTLSEnabled = true
	assert.Nil(t, rt.initActors())

	rt.running.Store(true)
	go sendSigterm(rt)
	rt.WaitUntilShutdown()

	var activeActCount int32
	activeActors := rt.actor.GetActiveActorsCount(context.Background())
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
	rt, err := NewTestDaprRuntime(modes.StandaloneMode)
	require.NoError(t, err)
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
	require.NoError(t, rt.setupTracing(context.Background(), rt.hostAddress, tpStore))
	assert.NotNil(t, rt.tracerProvider)

	rt.running.Store(true)
	go sendSigterm(rt)
	rt.WaitUntilShutdown()
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
	rt, _ := NewTestDaprRuntime(modes.KubernetesMode)
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
	go rt.beginHTTPEndpointsUpdates(context.Background())

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
