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

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"github.com/dapr/components-contrib/lock"
	lock_loader "github.com/dapr/dapr/pkg/components/lock"

	"contrib.go.opencensus.io/exporter/zipkin"
	"github.com/ghodss/yaml"
	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/phayes/freeport"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/contenttype"
	"github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/kit/logger"

	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	bindings_loader "github.com/dapr/dapr/pkg/components/bindings"
	nr_loader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	state_loader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/cors"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/expr"
	pb "github.com/dapr/dapr/pkg/grpc/proxy/testservice"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/pkg/sentry/certs"
	daprt "github.com/dapr/dapr/pkg/testing"
)

const (
	TestRuntimeConfigID  = "consumer0"
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	TestLockName         = "testlock"
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
					MaxRetries:  1,
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

func (m *MockKubernetesStateStore) BulkGetSecret(req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
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

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func NewMockKubernetesStoreWithInitCallback(cb func()) secretstores.SecretStore {
	return &MockKubernetesStateStore{callback: cb}
}

func TestNewRuntime(t *testing.T) {
	// act
	r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

// helper to populate subscription array for 2 pubsubs.
// 'topics' are the topics for the first pubsub.
// 'topics2' are the topics for the second pubsub.
func getSubscriptionsJSONString(topics []string, topics2 []string) string {
	s := []runtime_pubsub.SubscriptionJSON{}
	for _, t := range topics {
		s = append(s, runtime_pubsub.SubscriptionJSON{
			PubsubName: TestPubsubName,
			Topic:      t,
			Routes: runtime_pubsub.RoutesJSON{
				Default: t,
			},
		})
	}

	for _, t := range topics2 {
		s = append(s, runtime_pubsub.SubscriptionJSON{
			PubsubName: TestSecondPubsubName,
			Topic:      t,
			Routes: runtime_pubsub.RoutesJSON{
				Default: t,
			},
		})
	}
	b, _ := json.Marshal(&s)

	return string(b)
}

func getSubscriptionCustom(topic, path string) string {
	s := []runtime_pubsub.SubscriptionJSON{
		{
			PubsubName: TestPubsubName,
			Topic:      topic,
			Routes: runtime_pubsub.RoutesJSON{
				Default: path,
			},
		},
	}
	b, _ := json.Marshal(&s)
	return string(b)
}

func testDeclarativeSubscription() subscriptionsapi.Subscription {
	return subscriptionsapi.Subscription{
		TypeMeta: meta_v1.TypeMeta{
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

func writeSubscriptionToDisk(subscription subscriptionsapi.Subscription, filePath string) {
	b, _ := yaml.Marshal(subscription)
	os.WriteFile(filePath, b, 0o600)
}

func TestProcessComponentsAndDependents(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	incorrectComponentType := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
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

	pubsubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	lockComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestLockName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "lock.mockLock",
			Version: "v1",
		},
	}

	t.Run("test error on lock init", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(gomock.Any()).Return(assert.AnError)

		rt.lockStoreRegistry.Register(
			lock_loader.New("mockLock", func() lock.Store {
				return mockLockStore
			}),
		)

		// act
		err := rt.doProcessOneComponent(ComponentCategory("lock"), lockComponent)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, assert.AnError.Error(), err.Error(), "expected error strings to match")
	})

	t.Run("test error when lock version invalid", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)

		rt.lockStoreRegistry.Register(
			lock_loader.New("mockLock", func() lock.Store {
				return mockLockStore
			}),
		)

		lockComponentV3 := lockComponent
		lockComponentV3.Spec.Version = "v3"

		// act
		err := rt.doProcessOneComponent(ComponentCategory("lock"), lockComponentV3)

		// assert
		assert.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), "couldn't find lock store lock.mockLock/v3")
	})

	t.Run("test error when lock prefix strategy invalid", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(gomock.Any()).Return(nil)

		rt.lockStoreRegistry.Register(
			lock_loader.New("mockLock", func() lock.Store {
				return mockLockStore
			}),
		)

		lockComponentWithWrongStrategy := lockComponent
		lockComponentWithWrongStrategy.Spec.Metadata = []components_v1alpha1.MetadataItem{
			{
				Name: "keyPrefix",
				Value: components_v1alpha1.DynamicValue{
					JSON: v1.JSON{Raw: []byte("||")},
				},
			},
		}
		// act
		err := rt.doProcessOneComponent(ComponentCategory("lock"), lockComponentWithWrongStrategy)
		// assert
		assert.Error(t, err)
	})

	t.Run("lock init successfully and set right strategy", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(gomock.Any()).Return(nil)

		rt.lockStoreRegistry.Register(
			lock_loader.New("mockLock", func() lock.Store {
				return mockLockStore
			}),
		)

		// act
		err := rt.doProcessOneComponent(ComponentCategory("lock"), lockComponent)
		// assert
		assert.Nil(t, err, "unexpected error")
		// get modified key
		key, err := lock_loader.GetModifiedLockKey("test", "mockLock", "appid-1")
		assert.Nil(t, err, "unexpected error")
		assert.Equal(t, key, "lock||appid-1||test")
	})

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

	t.Run("test invalid category component", func(t *testing.T) {
		// act
		err := rt.doProcessOneComponent(ComponentCategory("invalid"), pubsubComponent)

		// assert
		assert.NoError(t, err, "no error expected")
	})
}

// mockOperatorClient is a mock implementation of operatorv1pb.OperatorClient.
// It is used to test `beginComponentsUpdates`.
type mockOperatorClient struct {
	operatorv1pb.OperatorClient

	lock                      sync.RWMutex
	compsByName               map[string]*components_v1alpha1.Component
	clientStreams             []*mockOperatorComponentUpdateClientStream
	clientStreamCreateWait    chan struct{}
	clientStreamCreatedNotify chan struct{}
}

func newMockOperatorClient() *mockOperatorClient {
	mockOpCli := &mockOperatorClient{
		compsByName:               make(map[string]*components_v1alpha1.Component),
		clientStreams:             make([]*mockOperatorComponentUpdateClientStream, 0, 1),
		clientStreamCreateWait:    make(chan struct{}, 1),
		clientStreamCreatedNotify: make(chan struct{}, 1),
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

func (c *mockOperatorClient) ClientStreamCount() int {
	c.lock.RLock()
	defer c.lock.RUnlock()

	return len(c.clientStreams)
}

func (c *mockOperatorClient) AllowOneNewClientStreamCreate() {
	c.clientStreamCreateWait <- struct{}{}
}

func (c *mockOperatorClient) WaitOneNewClientStreamCreated(ctx context.Context) error {
	select {
	case <-c.clientStreamCreatedNotify:
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

func (c *mockOperatorClient) UpdateComponent(comp *components_v1alpha1.Component) {
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

type mockOperatorComponentUpdateClientStream struct {
	operatorv1pb.Operator_ComponentUpdateClient

	updateCh chan *operatorv1pb.ComponentUpdateEvent
}

func (cs *mockOperatorComponentUpdateClientStream) Recv() (*operatorv1pb.ComponentUpdateEvent, error) {
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
			rt.appendOrReplaceComponents(comp)
			processedCh <- struct{}{}
		}
	}
	go mockProcessComponents()

	go rt.beginComponentsUpdates()

	comp1 := &components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "mockPubSub1",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "pubsub.mockPubSub1",
			Version: "v1",
		},
	}
	comp2 := &components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "mockPubSub2",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "pubsub.mockPubSub2",
			Version: "v1",
		},
	}
	comp3 := &components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "mockPubSub3",
		},
		Spec: components_v1alpha1.ComponentSpec{
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
	_, exists := rt.getComponent(comp1.Spec.Type, comp1.Name)
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
	_, exists = rt.getComponent(comp2.Spec.Type, comp2.Name)
	assert.True(t, exists, fmt.Sprintf("Expect component, type: %s, name: %s", comp2.Spec.Type, comp2.Name))

	mockOpCli.UpdateComponent(comp3)

	// Wait comp3 received and processed.
	select {
	case <-processedCh:
	case <-time.After(time.Second * 10):
		t.Errorf("Expect component [comp3] processed.")
		t.FailNow()
	}
	_, exists = rt.getComponent(comp3.Spec.Type, comp3.Name)
	assert.True(t, exists, fmt.Sprintf("Expect component, type: %s, name: %s", comp3.Spec.Type, comp3.Name))
}

func TestInitState(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	bytes := make([]byte, 32)
	rand.Read(bytes)

	primaryKey := hex.EncodeToString(bytes)

	mockStateComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "state.mockState",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "actorStateStore",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{Raw: []byte("true")},
					},
				},
				{
					Name: "primaryEncryptionKey",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{Raw: []byte(primaryKey)},
					},
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "mockSecretStore",
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
				actorStateStore:        "true",
				"primaryEncryptionKey": primaryKey,
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

	t.Run("test init state store, encryption not enabled", func(t *testing.T) {
		// setup
		initMockStateStoreForRuntime(rt, nil)

		// act
		err := rt.initState(mockStateComponent)
		ok := encryption.EncryptedStateStore("mockState")

		// assert
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("test init state store, encryption enabled", func(t *testing.T) {
		// setup
		initMockStateStoreForRuntime(rt, nil)

		rt.secretStores["mockSecretStore"] = &mockSecretStore{}

		err := rt.initState(mockStateComponent)
		ok := encryption.EncryptedStateStore("testpubsub")

		// assert
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}

func TestInitNameResolution(t *testing.T) {
	initMockResolverForRuntime := func(rt *DaprRuntime, resolverName string, e error) *daprt.MockResolver {
		mockResolver := new(daprt.MockResolver)

		rt.nameResolutionRegistry.Register(
			nr_loader.New(resolverName, func() nameresolution.Resolver {
				return mockResolver
			}),
		)

		expectedMetadata := nameresolution.Metadata{
			Properties: map[string]string{
				nameresolution.DaprHTTPPort:        strconv.Itoa(rt.runtimeConfig.HTTPPort),
				nameresolution.DaprPort:            strconv.Itoa(rt.runtimeConfig.InternalGRPCPort),
				nameresolution.AppPort:             strconv.Itoa(rt.runtimeConfig.ApplicationPort),
				nameresolution.HostAddress:         rt.hostAddress,
				nameresolution.AppID:               rt.runtimeConfig.ID,
				nameresolution.MDNSInstanceName:    rt.runtimeConfig.ID,
				nameresolution.MDNSInstanceAddress: rt.hostAddress,
				nameresolution.MDNSInstancePort:    strconv.Itoa(rt.runtimeConfig.InternalGRPCPort),
			},
		}

		mockResolver.On("Init", expectedMetadata).Return(e)

		return mockResolver
	}

	t.Run("error on unknown resolver", func(t *testing.T) {
		// given
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec.Component = "targetResolver"

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
		rt.globalConfig.Spec.NameResolutionSpec.Component = "someResolver"

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
		rt.globalConfig.Spec.NameResolutionSpec.Component = ""

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
		rt.globalConfig.Spec.NameResolutionSpec.Component = ""

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
			defer stopRuntime(t, rt)
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

func TestMetadataUUID(t *testing.T) {
	pubsubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		components_v1alpha1.MetadataItem{
			Name: "consumerID",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{uuid}"),
				},
			},
		}, components_v1alpha1.MetadataItem{
			Name: "twoUUIDs",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{uuid} {uuid}"),
				},
			},
		})
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.pubSubRegistry.Register(
		pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
			return mockPubSub
		}),
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		uuid0, err := uuid.Parse(consumerID)
		assert.Nil(t, err)

		twoUUIDs := metadata.Properties["twoUUIDs"]
		uuids := strings.Split(twoUUIDs, " ")
		assert.Equal(t, 2, len(uuids))
		uuid1, err := uuid.Parse(uuids[0])
		assert.Nil(t, err)
		uuid2, err := uuid.Parse(uuids[1])
		assert.Nil(t, err)

		assert.NotEqual(t, uuid0, uuid1)
		assert.NotEqual(t, uuid0, uuid2)
		assert.NotEqual(t, uuid1, uuid2)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.Nil(t, err)
}

func TestMetadataPodName(t *testing.T) {
	pubsubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	pubsubComponent.Spec.Metadata = append(
		pubsubComponent.Spec.Metadata,
		components_v1alpha1.MetadataItem{
			Name: "consumerID",
			Value: components_v1alpha1.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("{podName}"),
				},
			},
		})
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.pubSubRegistry.Register(
		pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
			return mockPubSub
		}),
	)

	rt.podName = "testPodName"

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]

		assert.Equal(t, "testPodName", consumerID)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.Nil(t, err)
}

func TestOnComponentUpdated(t *testing.T) {
	t.Run("component spec changed, component is updated", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.KubernetesMode)
		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "name1",
						Value: components_v1alpha1.DynamicValue{
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

		updated := rt.onComponentUpdated(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "name1",
						Value: components_v1alpha1.DynamicValue{
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
		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "name1",
						Value: components_v1alpha1.DynamicValue{
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

		updated := rt.onComponentUpdated(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "test",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "pubsub.mockPubSub",
				Version: "v1",
				Metadata: []components_v1alpha1.MetadataItem{
					{
						Name: "name1",
						Value: components_v1alpha1.DynamicValue{
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
	metadata := []components_v1alpha1.MetadataItem{
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
	}
	pubsubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: metadata,
		},
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	mockPubSub := new(daprt.MockPubSub)

	rt.pubSubRegistry.Register(
		pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
			return mockPubSub
		}),
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		assert.Equal(t, TestRuntimeConfigID, consumerID)
	})

	err := rt.processComponentAndDependents(pubsubComponent)
	assert.Nil(t, err)
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	pubsubComponents := []components_v1alpha1.Component{
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Version:  "v1",
				Metadata: getFakeMetadataItems(),
			},
		}, {
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestSecondPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub2",
				Version:  "v1",
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
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

		mockPubSub2.On("Init", expectedMetadata).Return(nil)
		mockPubSub2.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

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
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)
		a := rts.getPublishAdapter()
		assert.Nil(t, a)
	})

	t.Run("publish adapter not nil, with pub sub component", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)
		rts.pubSubs[TestPubsubName], _ = initMockPubSubForRuntime(rts)
		a := rts.getPublishAdapter()
		assert.NotNil(t, a)
	})

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		rts := NewTestDaprRuntime(modes.StandaloneMode)
		rts.appChannel = nil
		routes, err := rts.getTopicRoutes()
		assert.Nil(t, err)
		assert.Equal(t, 0, len(routes))
	})

	t.Run("load declarative subscription, no scopes", func(t *testing.T) {
		dir := "./components"

		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(dir, 0o777))
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rts.runtimeConfig.Standalone.ComponentsPath = dir
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
		dir := "./components"

		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(dir, 0o777))
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{TestRuntimeConfigID}

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rts.runtimeConfig.Standalone.ComponentsPath = dir
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
		dir := "./components"

		rts := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rts)

		require.NoError(t, os.Mkdir(dir, 0o777))
		defer os.RemoveAll(dir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{"scope1"}

		filePath := "./components/sub.yaml"
		writeSubscriptionToDisk(s, filePath)

		rts.runtimeConfig.Standalone.ComponentsPath = dir
		subs := rts.getDeclarativeSubscriptions()
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
		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
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
		defer stopRuntime(t, rt)
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
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		err := rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)
		assert.NotNil(t, rt.secretStores["kubernetesMock"])
	})

	t.Run("get secret store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})

		s := rt.getSecretStore("kubernetesMock")
		assert.NotNil(t, s)
	})
}

func TestMetadataItemsToPropertiesConversion(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
			Type:    "bindings.mock",
			Version: "v1",
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
		defer stopRuntime(t, rt)
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
				Type:    "secretstores.kubernetes",
				Version: "v1",
			},
		})

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Kubernetes Mode - no value without operator", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = components_v1alpha1.DynamicValue{
			JSON: v1.JSON{Raw: []byte("")},
		}
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Key:  "key1",
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)
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
		assert.Equal(t, "", mod.Spec.Metadata[0].Value.String())
		assert.Empty(t, unready)
	})

	t.Run("Look up name only", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = components_v1alpha1.DynamicValue{
			JSON: v1.JSON{Raw: []byte("")},
		}
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Name: "name1",
		}
		mockBinding.Auth.SecretStore = "mock"

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		defer stopRuntime(t, rt)

		rt.secretStoresRegistry.Register(
			secretstores_loader.New("mock", func() secretstores.SecretStore {
				return &mockSecretStore{}
			}),
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.processComponentAndDependents(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "mock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:    "secretstores.mock",
				Version: "v1",
			},
		})
		assert.NoError(t, err)

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
		{"state.redis", "state"},
		{"states.redis", ""},
		{"bindings.kafka", "bindings"},
		{"binding.kafka", ""},
		{"this.is.invalid.category", ""},
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	for _, tt := range compCategoryTests {
		t.Run(tt.specType, func(t *testing.T) {
			fakeComp := components_v1alpha1.Component{
				Spec: components_v1alpha1.ComponentSpec{
					Type:    tt.specType,
					Version: "v1",
				},
			}
			assert.Equal(t, string(rt.extractComponentCategory(fakeComp)), tt.category)
		})
	}
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
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
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
				Type:    "secretstores.kubernetesMockGrandChild",
				Version: "v1",
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
				Type:    "secretstores.kubernetesMockChild",
				Version: "v1",
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
	rt := NewTestDaprRuntime(modes.KubernetesMode)
	defer stopRuntime(t, rt)

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

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.topic)
	fakeReq.WithHTTPExtension(http.MethodPost, "")
	fakeReq.WithRawData(testPubSubMessage.data, contenttype.CloudEventContentType)
	fakeReq.WithCustomHTTPMetadata(testPubSubMessage.metadata)

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.topicRoutes = map[string]TopicRoute{}
	rt.topicRoutes[TestPubsubName] = TopicRoute{routes: make(map[string]Route)}
	rt.topicRoutes[TestPubsubName].routes["topic1"] = Route{rules: []*runtime_pubsub.Rule{{Path: "topic1"}}}

	t.Run("ok without result body", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

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

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"RETRY\"}"), "application/json")

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Error(t, err)
	})

	t.Run("ok with drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"DROP\"}"), "application/json")

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.NoError(t, err)
	})

	t.Run("ok with unknown", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"UNKNOWN\"}"), "application/json")

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

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.topic)
	fakeReq.WithHTTPExtension(http.MethodPost, "")
	fakeReq.WithRawData(testPubSubMessage.data, contenttype.CloudEventContentType)

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.topicRoutes = map[string]TopicRoute{}
	rt.topicRoutes[TestPubsubName] = TopicRoute{routes: make(map[string]Route)}
	rt.topicRoutes[TestPubsubName].routes["topic1"] = Route{rules: []*runtime_pubsub.Rule{{Path: "topic1"}}}

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
						return errors.Errorf("unexpected reply type: %s", reflect.TypeOf(reply))
					}

					response.Status = tc.Status

					return nil
				},
			}
			rt.grpc.AppClient = &mockClientConn

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
	assert.Nil(t, err)

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       b,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.topic)
	fakeReq.WithHTTPExtension(http.MethodPost, "")
	fakeReq.WithRawData(testPubSubMessage.data, contenttype.CloudEventContentType)
	fakeReq.WithCustomHTTPMetadata(testPubSubMessage.metadata)

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)
	rt.topicRoutes = map[string]TopicRoute{}
	rt.topicRoutes[TestPubsubName] = TopicRoute{routes: make(map[string]Route)}
	rt.topicRoutes[TestPubsubName].routes["topic1"] = Route{rules: []*runtime_pubsub.Rule{{Path: "topic1"}}}

	t.Run("succeeded to publish message to user app with empty response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message without TraceID", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		// Generate a new envelope to avoid affecting other tests by modifying shared `envelope`
		envelopeNoTraceID := pubsub.NewCloudEventsEnvelope(
			"", "", pubsub.DefaultCloudEventType, "", topic, TestSecondPubsubName, "",
			[]byte("Test Message"), "", "")
		delete(envelopeNoTraceID, pubsub.TraceIDField)
		bNoTraceID, err := json.Marshal(envelopeNoTraceID)
		assert.Nil(t, err)

		message := &pubsubSubscribedMessage{
			cloudEvent: envelopeNoTraceID,
			topic:      topic,
			data:       bNoTraceID,
			metadata:   map[string]string{pubsubName: TestPubsubName},
			path:       "topic1",
		}

		fakeReqNoTraceID := invokev1.NewInvokeMethodRequest(message.topic)
		fakeReqNoTraceID.WithHTTPExtension(http.MethodPost, "")
		fakeReqNoTraceID.WithRawData(message.data, contenttype.CloudEventContentType)
		fakeReqNoTraceID.WithCustomHTTPMetadata(testPubSubMessage.metadata)
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReqNoTraceID).Return(fakeResp, nil)

		// act
		err = rt.publishMessageHTTP(context.Background(), message)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.data, &cloudEvent)
		expectedClientError := errors.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent["id"].(string))
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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

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
		err := rt.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.data, &cloudEvent)
		expectedClientError := errors.Errorf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: Internal Error. status code returned: 500", cloudEvent["id"].(string), cloudEvent["topic"])
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestOnNewPublishedMessageGRPC(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic,
		TestSecondPubsubName, "", []byte("Test Message"), "", "")
	b, err := json.Marshal(envelope)
	assert.Nil(t, err)

	testPubSubMessage := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       b,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	envelope = pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic,
		TestSecondPubsubName, "application/octet-stream", []byte{0x1}, "", "")
	base64, err := json.Marshal(envelope)
	assert.Nil(t, err)

	testPubSubMessageBase64 := &pubsubSubscribedMessage{
		cloudEvent: envelope,
		topic:      topic,
		data:       base64,
		metadata:   map[string]string{pubsubName: TestPubsubName},
		path:       "topic1",
	}

	testCases := []struct {
		name             string
		message          *pubsubSubscribedMessage
		responseStatus   runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		errorExpected    bool
		noResponseStatus bool
		responseError    error
	}{
		{
			name:             "failed to publish message to user app with unimplemented error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    status.Errorf(codes.Unimplemented, "unimplemented method"),
			errorExpected:    false, // should be dropped with no error
		},
		{
			name:             "failed to publish message to user app with response error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    assert.AnError,
			errorExpected:    true,
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
			errorExpected:  true,
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
					topic: {rules: []*runtime_pubsub.Rule{{Path: topic}}},
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
			err = rt.publishMessageGRPC(context.Background(), tc.message)

			// assert
			if tc.errorExpected {
				assert.Error(t, err, "expected an error")
			} else {
				assert.Nil(t, err, "expected no error")
			}
		})
	}
}

func TestPubsubWithResiliency(t *testing.T) {
	r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency))
	defer stopRuntime(t, r)

	failingPubsub := daprt.FailingPubsub{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"failingTopic": 1,
			},
			Timeouts: map[string]time.Duration{
				"timeoutTopic": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
	}

	failingAppChannel := daprt.FailingAppChannel{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"failingSubTopic": 1,
			},
			Timeouts: map[string]time.Duration{
				"timeoutSubTopic": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			rawData := req.Message().Data.Value
			data := make(map[string]string)
			json.Unmarshal(rawData, &data)
			val, _ := base64.StdEncoding.DecodeString(data["data_base64"])
			return string(val)
		},
	}

	r.pubSubRegistry.Register(pubsub_loader.New(
		"failingPubsub", func() pubsub.PubSub { return &failingPubsub },
	))

	component := components_v1alpha1.Component{}
	component.ObjectMeta.Name = "failPubsub"
	component.Spec.Type = "pubsub.failingPubsub"

	err := r.initPubSub(component)
	assert.NoError(t, err)

	t.Run("pubsub publish retries with resiliency", func(t *testing.T) {
		req := &pubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "failingTopic",
		}
		err := r.Publish(req)

		assert.NoError(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount["failingTopic"])
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
		assert.Equal(t, 2, failingPubsub.Failure.CallCount["timeoutTopic"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	r.runtimeConfig.ApplicationProtocol = HTTPProtocol
	r.appChannel = &failingAppChannel

	t.Run("pubsub retries subscription event with resiliency", func(t *testing.T) {
		r.topicRoutes = make(map[string]TopicRoute)
		r.topicRoutes["failPubsub"] = TopicRoute{routes: map[string]Route{
			"failingSubTopic": {
				metadata: map[string]string{
					"rawPayload": "true",
				},
				rules: []*runtime_pubsub.Rule{
					{
						Path: "failingPubsub",
					},
				},
			},
		}}

		err := r.beginPubSub(context.Background(), "failPubsub", &failingPubsub)

		assert.NoError(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount["failingSubTopic"])
	})

	t.Run("pubsub times out sending event to app with resiliency", func(t *testing.T) {
		r.topicRoutes = make(map[string]TopicRoute)
		r.topicRoutes["failPubsub"] = TopicRoute{routes: map[string]Route{
			"timeoutSubTopic": {
				metadata: map[string]string{
					"rawPayload": "true",
				},
				rules: []*runtime_pubsub.Rule{
					{
						Path: "failingPubsub",
					},
				},
			},
		}}

		start := time.Now()
		err := r.beginPubSub(context.Background(), "failPubsub", &failingPubsub)
		end := time.Now()

		// This is eaten, technically.
		assert.NoError(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount["timeoutSubTopic"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

// mockSubscribePubSub is an in-memory pubsub component.
type mockSubscribePubSub struct {
	handlers map[string]pubsub.Handler
	pubCount map[string]int
}

// Init is a mock initialization method.
func (m *mockSubscribePubSub) Init(metadata pubsub.Metadata) error {
	m.handlers = make(map[string]pubsub.Handler)
	m.pubCount = make(map[string]int)
	return nil
}

// Publish is a mock publish method. Immediately trigger handler if topic is subscribed.
func (m *mockSubscribePubSub) Publish(req *pubsub.PublishRequest) error {
	m.pubCount[req.Topic]++
	if handler, ok := m.handlers[req.Topic]; ok {
		pubsubMsg := &pubsub.NewMessage{
			Data:  req.Data,
			Topic: req.Topic,
		}
		handler(context.Background(), pubsubMsg)
	}

	return nil
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
	return nil
}

func TestPubSubDeadLetter(t *testing.T) {
	testDeadLetterPubsub := "failPubsub"
	pubsubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: testDeadLetterPubsub,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: getFakeMetadataItems(),
		},
	}

	t.Run("succeeded to publish message to dead letter when send message to app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return &mockSubscribePubSub{}
			}),
		)
		req := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		req.WithHTTPExtension(http.MethodGet, "")
		req.WithRawData(nil, invokev1.JSONContentType)

		subscriptionItems := []runtime_pubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData(sub, "application/json")

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), req).Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), mock.Anything).Return(nil, errors.New("failed to send"))

		require.NoError(t, rt.initPubSub(pubsubComponent))
		rt.startSubscribing()

		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		assert.Nil(t, err)
		pubsubIns := rt.pubSubs[testDeadLetterPubsub].(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.pubCount["topic0"])
		// Ensure the message is sent to dead letter topic.
		assert.Equal(t, 1, pubsubIns.pubCount["topic1"])
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
	})

	t.Run("use dead letter with resiliency", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.resiliency = resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency)
		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return &mockSubscribePubSub{}
			}),
		)
		req := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		req.WithHTTPExtension(http.MethodGet, "")
		req.WithRawData(nil, invokev1.JSONContentType)

		subscriptionItems := []runtime_pubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData(sub, "application/json")

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), req).Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.timerCtx"), mock.Anything).Return(nil, errors.New("failed to send"))

		require.NoError(t, rt.initPubSub(pubsubComponent))
		rt.startSubscribing()

		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		assert.Nil(t, err)
		pubsubIns := rt.pubSubs[testDeadLetterPubsub].(*mockSubscribePubSub)
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
		[]string{DefaultAPIListenAddress},
		nil,
		appPort,
		DefaultProfilePort,
		false,
		-1,
		false,
		"",
		false,
		4,
		"",
		4,
		false,
		time.Second,
		true,
		true)

	return NewDaprRuntime(testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
}

func TestGracefulShutdown(t *testing.T) {
	r := NewTestDaprRuntime(modes.StandaloneMode)
	assert.Equal(t, time.Second, r.runtimeConfig.GracefulShutdownDuration)
}

func TestMTLS(t *testing.T) {
	t.Run("with mTLS enabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.runtimeConfig.mtlsEnabled = true
		rt.runtimeConfig.SentryServiceAddress = "1.1.1.1"

		t.Setenv(certs.TrustAnchorsEnvVar, testCertRoot)
		t.Setenv(certs.CertChainEnvVar, "a")
		t.Setenv(certs.CertKeyEnvVar, "b")

		certChain, err := security.GetCertChain()
		assert.Nil(t, err)
		rt.runtimeConfig.CertChain = certChain

		err = rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
		assert.NotNil(t, rt.authenticator)
	})

	t.Run("with mTLS disabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)

		err := rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
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
	hasError bool
	data     string
	metadata map[string]string
	closeErr error
}

func (b *mockBinding) Init(metadata bindings.Metadata) error {
	return nil
}

func (b *mockBinding) Read(handler bindings.Handler) error {
	b.data = string(testInputBindingData)
	metadata := map[string]string{}
	if b.metadata != nil {
		metadata = b.metadata
	}

	_, err := handler(context.TODO(), &bindings.ReadResponse{
		Metadata: metadata,
		Data:     []byte(b.data),
	})
	b.hasError = err != nil
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
		rt.outputBindings["mockBinding"] = &mockBinding{}

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.CreateOperation,
		})
		assert.Nil(t, err)
	})

	t.Run("output binding invalid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.outputBindings["mockBinding"] = &mockBinding{}

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

		rt.inputBindingRoutes[testInputBindingName] = testInputBindingName

		b := mockBinding{}
		rt.readFromBinding(testInputBindingName, &b)

		assert.False(t, b.hasError)
	})

	t.Run("app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
		rt.inputBindingRoutes[testInputBindingName] = testInputBindingName

		b := mockBinding{}
		rt.readFromBinding(testInputBindingName, &b)

		assert.True(t, b.hasError)
	})

	t.Run("binding has data and metadata", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
		rt.inputBindingRoutes[testInputBindingName] = testInputBindingName

		b := mockBinding{metadata: map[string]string{"bindings": "input"}}
		rt.readFromBinding(testInputBindingName, &b)

		assert.Equal(t, string(testInputBindingData), b.data)
	})
}

func TestNamespace(t *testing.T) {
	t.Run("empty namespace", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		ns := rt.getNamespace()

		assert.Empty(t, ns)
	})

	t.Run("non-empty namespace", func(t *testing.T) {
		t.Setenv("NAMESPACE", "a")

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		ns := rt.getNamespace()

		assert.Equal(t, "a", ns)
	})
}

func TestPodName(t *testing.T) {
	t.Run("empty podName", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		podName := rt.getPodName()

		assert.Empty(t, podName)
	})

	t.Run("non-empty podName", func(t *testing.T) {
		t.Setenv("POD_NAME", "testPodName")

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		podName := rt.getPodName()

		assert.Equal(t, "testPodName", podName)
	})
}

func TestAuthorizedComponents(t *testing.T) {
	testCompName := "fakeComponent"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
		assert.Equal(t, testCompName, comps[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
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
		defer stopRuntime(t, rt)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})
}

type mockPublishPubSub struct{}

// Init is a mock initialization method.
func (m *mockPublishPubSub) Init(metadata pubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method.
func (m *mockPublishPubSub) Publish(req *pubsub.PublishRequest) error {
	return nil
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

func TestInitActors(t *testing.T) {
	t.Run("missing namespace on kubernetes", func(t *testing.T) {
		r := NewDaprRuntime(&Config{Mode: modes.KubernetesMode}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
		r.namespace = ""
		r.runtimeConfig.mtlsEnabled = true

		err := r.initActors()
		assert.Error(t, err)
	})

	t.Run("actors hosted = true", func(t *testing.T) {
		r := NewDaprRuntime(&Config{Mode: modes.KubernetesMode}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
		r.appConfig = config.ApplicationConfig{
			Entities: []string{"actor1"},
		}

		hosted := len(r.appConfig.Entities) > 0
		assert.True(t, hosted)
	})

	t.Run("actors hosted = false", func(t *testing.T) {
		r := NewDaprRuntime(&Config{Mode: modes.KubernetesMode}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		hosted := len(r.appConfig.Entities) > 0
		assert.False(t, hosted)
	})

	t.Run("placement enable = false", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		err := r.initActors()
		assert.NotNil(t, err)
	})

	t.Run("the state stores can still be initialized normally", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		assert.Nil(t, r.actor)
		assert.NotNil(t, r.stateStores)
	})

	t.Run("the actor store can not be initialized normally", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)

		assert.Equal(t, "", r.actorStateStoreName)
		err := r.initActors()
		assert.NotNil(t, err)
	})
}

func TestInitBindings(t *testing.T) {
	t.Run("single input binding", func(t *testing.T) {
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
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
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
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
		r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		defer stopRuntime(t, r)
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

func TestBindingResiliency(t *testing.T) {
	r := NewDaprRuntime(&Config{}, &config.Configuration{}, &config.AccessControlList{}, resiliency.FromConfigurations(logger.NewLogger("test"), testResiliency))
	defer stopRuntime(t, r)

	failingChannel := daprt.FailingAppChannel{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"inputFailingKey": 1,
			},
			Timeouts: map[string]time.Duration{
				"inputTimeoutKey": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			return string(req.Message().Data.Value)
		},
	}

	r.appChannel = &failingChannel
	r.runtimeConfig.ApplicationProtocol = HTTPProtocol

	failingBinding := daprt.FailingBinding{
		Failure: daprt.Failure{
			Fails: map[string]int{
				"outputFailingKey": 1,
			},
			Timeouts: map[string]time.Duration{
				"outputTimeoutKey": time.Second * 10,
			},
			CallCount: map[string]int{},
		},
	}

	r.bindingsRegistry.RegisterOutputBindings(
		bindings_loader.NewOutput("failingoutput", func() bindings.OutputBinding {
			return &failingBinding
		}),
	)

	output := components_v1alpha1.Component{}
	output.ObjectMeta.Name = "failOutput"
	output.Spec.Type = "bindings.failingoutput"
	err := r.initBinding(output)
	assert.NoError(t, err)

	t.Run("output binding retries on failure with resiliency", func(t *testing.T) {
		req := &bindings.InvokeRequest{
			Data:      []byte("outputFailingKey"),
			Operation: "create",
		}
		_, err := r.sendToOutputBinding("failOutput", req)

		assert.Nil(t, err)
		assert.Equal(t, 2, failingBinding.Failure.CallCount["outputFailingKey"])
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
		assert.Equal(t, 2, failingBinding.Failure.CallCount["outputTimeoutKey"])
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("input binding retries on failure with resiliency", func(t *testing.T) {
		_, err := r.sendBindingEventToApp("failingInputBinding", []byte("inputFailingKey"), map[string]string{})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount["inputFailingKey"])
	})

	t.Run("input binding times out with resiliency", func(t *testing.T) {
		start := time.Now()
		_, err := r.sendBindingEventToApp("failingInputBinding", []byte("inputTimeoutKey"), map[string]string{})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingChannel.Failure.CallCount["inputTimeoutKey"])
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
			r := NewDaprRuntime(&Config{Mode: modes.KubernetesMode}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

			mockAppChannel := new(channelt.MockAppChannel)
			r.appChannel = mockAppChannel
			r.runtimeConfig.ApplicationProtocol = HTTPProtocol

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

func (p *mockPubSub) Init(metadata pubsub.Metadata) error {
	return nil
}

func (p *mockPubSub) Close() error {
	return p.closeErr
}

type mockStateStore struct {
	state.Store
	closeErr error
}

func (s *mockStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (s *mockStateStore) Close() error {
	return s.closeErr
}

type mockSecretStore struct {
	secretstores.SecretStore
	closeErr error
}

func (s *mockSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (s *mockSecretStore) Init(metadata secretstores.Metadata) error {
	return nil
}

func (s *mockSecretStore) Close() error {
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

	rt.bindingsRegistry.RegisterInputBindings(
		bindings_loader.NewInput("input", func() bindings.InputBinding {
			return &mockBinding{closeErr: testErr}
		}),
	)
	rt.bindingsRegistry.RegisterOutputBindings(
		bindings_loader.NewOutput("output", func() bindings.OutputBinding {
			return &mockBinding{closeErr: testErr}
		}),
	)
	rt.pubSubRegistry.Register(
		pubsub_loader.New("pubsub", func() pubsub.PubSub {
			return &mockPubSub{closeErr: testErr}
		}),
	)
	rt.stateStoreRegistry.Register(
		state_loader.New("statestore", func() state.Store {
			return &mockStateStore{closeErr: testErr}
		}),
	)
	rt.secretStoresRegistry.Register(
		secretstores_loader.New("secretstore", func() secretstores.SecretStore {
			return &mockSecretStore{closeErr: testErr}
		}),
	)

	mockInputBindingComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "bindings.input",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "input",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockOutputBindingComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "bindings.output",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "output",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockPubSubComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "pubsub.pubsub",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "pubsub",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockStateComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "state.statestore",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "statestore",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}
	mockSecretsComponent := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type:    "secretstores.secretstore",
			Version: "v1",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "secretstore",
					Value: components_v1alpha1.DynamicValue{
						JSON: v1.JSON{},
					},
				},
			},
		},
	}

	require.NoError(t, rt.initInputBinding(mockInputBindingComponent))
	require.NoError(t, rt.initOutputBinding(mockOutputBindingComponent))
	require.NoError(t, rt.initPubSub(mockPubSubComponent))
	require.NoError(t, rt.initState(mockStateComponent))
	require.NoError(t, rt.initSecretStore(mockSecretsComponent))
	rt.nameResolver = &mockNameResolver{closeErr: testErr}

	err := rt.shutdownOutputComponents()
	assert.Error(t, err)
	var merr *multierror.Error
	merr, ok := err.(*multierror.Error)
	require.True(t, ok)
	assert.Equal(t, 6, len(merr.Errors))
}

func stopRuntime(t *testing.T, rt *DaprRuntime) {
	rt.stopActor()
	assert.NoError(t, rt.shutdownOutputComponents())
}

func TestFindMatchingRoute(t *testing.T) {
	r, err := createRoutingRule(`event.type == "MyEventType"`, "mypath")
	require.NoError(t, err)
	rules := []*runtime_pubsub.Rule{r}
	path, shouldProcess, err := findMatchingRoute(rules, map[string]interface{}{
		"type": "MyEventType",
	})
	require.NoError(t, err)
	assert.Equal(t, "mypath", path)
	assert.True(t, shouldProcess)
}

func createRoutingRule(match, path string) (*runtime_pubsub.Rule, error) {
	var e *expr.Expr
	matchTrimmed := strings.TrimSpace(match)
	if matchTrimmed != "" {
		e = &expr.Expr{}
		if err := e.DecodeString(matchTrimmed); err != nil {
			return nil, err
		}
	}

	return &runtime_pubsub.Rule{
		Match: e,
		Path:  path,
	}, nil
}

func TestComponentsCallback(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "OK")
	}))
	defer svr.Close()

	u, err := url.Parse(svr.URL)
	require.NoError(t, err)
	port, _ := strconv.Atoi(u.Port())
	rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, "http", port)
	defer stopRuntime(t, rt)

	c := make(chan struct{})
	callbackInvoked := false

	rt.Run(WithComponentsCallback(func(components ComponentRegistry) error {
		close(c)
		callbackInvoked = true

		return nil
	}))
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

	mockNameResolution := nr_loader.NameResolution{
		Names: []string{"mdns"}, // for standalone mode
		FactoryMethod: func() nameresolution.Resolver {
			mockResolver := new(daprt.MockResolver)
			// proxy to server anytime
			mockResolver.On("Init", mock.Anything).Return(nil)
			mockResolver.On("ResolveID", mock.Anything).Return(fmt.Sprintf("localhost:%d", serverPort), nil)
			return mockResolver
		},
	}

	// setup proxy
	rt := NewTestDaprRuntimeWithProtocol(modes.StandaloneMode, "grpc", serverPort)
	internalPort, _ := freeport.GetFreePort()
	rt.runtimeConfig.InternalGRPCPort = internalPort
	defer stopRuntime(t, rt)

	go func() {
		rt.Run(WithNameResolutions(mockNameResolution))
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
	cPubSub := components_v1alpha1.Component{}
	cPubSub.ObjectMeta.Name = "mockPubSub"
	cPubSub.Spec.Type = "pubsub.mockPubSub"

	cStateStore := components_v1alpha1.Component{}
	cStateStore.ObjectMeta.Name = "testStateStoreName"
	cStateStore.Spec.Type = "state.mockState"

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	defer stopRuntime(t, rt)

	mockStateStore := new(daprt.MockStateStore)
	rt.stateStoreRegistry.Register(
		state_loader.New("mockState", func() state.Store {
			return mockStateStore
		}),
	)

	mockStateStore.On("Init", mock.Anything).Return(nil)

	mockPubSub := new(daprt.MockPubSub)
	rt.pubSubRegistry.Register(
		pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
			return mockPubSub
		}),
	)

	mockPubSub.On("Init", mock.Anything).Return(nil)

	rt.bindingsRegistry.RegisterInputBindings(
		bindings_loader.NewInput("testInputBinding", func() bindings.InputBinding {
			return &daprt.MockBinding{}
		}),
	)
	cin := components_v1alpha1.Component{}
	cin.ObjectMeta.Name = "testInputBinding"
	cin.Spec.Type = "bindings.testInputBinding"

	rt.bindingsRegistry.RegisterOutputBindings(
		bindings_loader.NewOutput("testOutputBinding", func() bindings.OutputBinding {
			return &daprt.MockBinding{}
		}),
	)
	cout := components_v1alpha1.Component{}
	cout.ObjectMeta.Name = "testOutputBinding"
	cout.Spec.Type = "bindings.testOutputBinding"

	require.NoError(t, rt.initInputBinding(cin))
	require.NoError(t, rt.initOutputBinding(cout))
	require.NoError(t, rt.initPubSub(cPubSub))
	require.NoError(t, rt.initState(cStateStore))

	capabilities := rt.getComponentsCapabilitesMap()
	assert.Equal(t, 3, len(capabilities))
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
