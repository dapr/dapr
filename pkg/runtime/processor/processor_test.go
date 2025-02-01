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

package processor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/security/fake"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// functional options
type newTestProcOptions func(*Options)

func withID(id string) newTestProcOptions {
	return func(o *Options) {
		o.ID = id
		o.Meta = meta.New(meta.Options{
			ID:        id,
			PodName:   "testPodName",
			Namespace: "test",
			Mode:      modes.StandaloneMode,
		})
	}
}

func withReporter(r registry.Reporter) newTestProcOptions {
	return func(o *Options) {
		o.Reporter = r
	}
}

func newTestProc(setters ...newTestProcOptions) (*Processor, *registry.Registry) {
	reg := registry.New(registry.NewOptions())
	opts := Options{
		ID:             "id",
		Namespace:      "test",
		Registry:       reg,
		ComponentStore: compstore.New(),
		Meta: meta.New(meta.Options{
			ID:        "id",
			PodName:   "testPodName",
			Namespace: "test",
			Mode:      modes.StandaloneMode,
		}),
		Resiliency:     resiliency.New(log),
		Mode:           modes.StandaloneMode,
		PodName:        "testPodName",
		OperatorClient: nil,
		GRPC:           nil,
		Channels:       new(channels.Channels),
		GlobalConfig:   new(config.Configuration),
		Security:       fake.New(),
		Reporter:       reg.Reporter(),
	}
	for _, setter := range setters {
		setter(&opts)
	}

	return New(opts), reg
}

func TestProcessComponentsAndDependents(t *testing.T) {
	proc, _ := newTestProc()
	incorrectComponentType := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
			Type:     "pubsubs.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	t.Run("test incorrect type", func(t *testing.T) {
		err := proc.processComponentAndDependents(context.Background(), incorrectComponentType)
		require.Error(t, err, "expected an error")
		assert.Equal(t, "incorrect type pubsubs.mockPubSub", err.Error(), "expected error strings to match")
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with store", func(t *testing.T) {
		proc, reg := newTestProc()
		m := rtmock.NewMockKubernetesStore()
		reg.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err := proc.processComponentAndDependents(context.Background(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		require.NoError(t, err)
	})

	t.Run("secret store is registered", func(t *testing.T) {
		proc, reg := newTestProc()
		m := rtmock.NewMockKubernetesStore()
		reg.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		err := proc.processComponentAndDependents(context.Background(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		require.NoError(t, err)
		store, ok := proc.compStore.GetSecretStore("kubernetesMock")
		assert.True(t, ok)
		assert.NotNil(t, store)
	})

	t.Run("get secret store", func(t *testing.T) {
		proc, reg := newTestProc()
		m := rtmock.NewMockKubernetesStore()
		reg.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		proc.processComponentAndDependents(context.Background(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})

		s, ok := proc.compStore.GetSecretStore("kubernetesMock")
		assert.True(t, ok)
		assert.NotNil(t, s)
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

	p := New(Options{
		Registry:     registry.New(registry.NewOptions()),
		GlobalConfig: new(config.Configuration),
	})

	for _, tt := range compCategoryTests {
		t.Run(tt.specType, func(t *testing.T) {
			fakeComp := componentsapi.Component{
				Spec: componentsapi.ComponentSpec{
					Type:    tt.specType,
					Version: "v1",
				},
			}
			assert.Equal(t, string(p.category(fakeComp)), tt.category)
		})
	}
}

func TestMetadataUUID(t *testing.T) {
	pubsubComponent := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
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
	proc, reg := newTestProc()
	mockPubSub := new(daprt.MockPubSub)

	reg.PubSubs().RegisterComponent(
		func(_ logger.Logger) pubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(pubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		var uuid0, uuid1, uuid2 uuid.UUID
		uuid0, err := uuid.Parse(consumerID)
		require.NoError(t, err)

		twoUUIDs := metadata.Properties["twoUUIDs"]
		uuids := strings.Split(twoUUIDs, " ")
		assert.Len(t, uuids, 2)
		uuid1, err = uuid.Parse(uuids[0])
		require.NoError(t, err)
		uuid2, err = uuid.Parse(uuids[1])
		require.NoError(t, err)

		assert.NotEqual(t, uuid0, uuid1)
		assert.NotEqual(t, uuid0, uuid2)
		assert.NotEqual(t, uuid1, uuid2)
	})

	err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
	require.NoError(t, err)
}

func TestMetadataPodName(t *testing.T) {
	t.Setenv("POD_NAME", "testPodName")

	pubsubComponent := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
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
	proc, reg := newTestProc()
	mockPubSub := new(daprt.MockPubSub)

	reg.PubSubs().RegisterComponent(
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

	err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
	require.NoError(t, err)
}

func TestMetadataNamespace(t *testing.T) {
	t.Setenv("NAMESPACE", "test")

	pubsubComponent := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
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

	proc, reg := newTestProc(withID("app1"))
	mockPubSub := new(daprt.MockPubSub)

	reg.PubSubs().RegisterComponent(
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

	err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
	require.NoError(t, err)
}

func TestMetadataClientID(t *testing.T) {
	pubsubComponent := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	// ClientID should be namespace.AppID for Kubernetes
	t.Run("Kubernetes Mode AppID", func(t *testing.T) {
		t.Setenv("NAMESPACE", "test")
		pubsubComponent.Spec.Metadata = append(
			pubsubComponent.Spec.Metadata,
			commonapi.NameValuePair{
				Name: "clientID",
				Value: commonapi.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("{namespace}"),
					},
				},
			})

		proc, reg := newTestProc(withID("myApp"))
		mockPubSub := new(daprt.MockPubSub)

		reg.PubSubs().RegisterComponent(
			func(_ logger.Logger) pubsub.PubSub {
				return mockPubSub
			},
			"mockPubSub",
		)

		var k8sClientID string
		clientIDChan := make(chan string, 1)
		mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			metadata := args.Get(0).(pubsub.Metadata)
			k8sClientID = metadata.Properties["clientID"]
			clientIDChan <- k8sClientID
		})

		err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
		require.NoError(t, err)

		select {
		case clientID := <-clientIDChan:
			assert.Equal(t, "test.myApp", clientID)
		case <-time.After(20 * time.Second):
			t.Error("Timed out waiting for clientID for Kubernetes Mode test")
		}
	})

	// ClientID should be AppID for Self-Hosted
	t.Run("Standalone Mode AppID", func(t *testing.T) {
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

		proc, reg := newTestProc(withID(daprt.TestRuntimeConfigID))
		mockPubSub := new(daprt.MockPubSub)

		reg.PubSubs().RegisterComponent(
			func(_ logger.Logger) pubsub.PubSub {
				return mockPubSub
			},
			"mockPubSub",
		)

		var standAloneClientID string
		clientIDChan := make(chan string, 1)
		mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			metadata := args.Get(0).(pubsub.Metadata)
			standAloneClientID = metadata.Properties["clientID"]
			clientIDChan <- standAloneClientID
		})

		err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
		require.NoError(t, err)
		appIds := strings.Split(standAloneClientID, " ")
		assert.Len(t, appIds, 2)
		for _, appID := range appIds {
			assert.Equal(t, daprt.TestRuntimeConfigID, appID)
		}

		select {
		case clientID := <-clientIDChan:
			assert.Equal(t, standAloneClientID, clientID)
		case <-time.After(20 * time.Second):
			t.Error("Timed out waiting for clientID for Standalone Mode test")
		}
	})
}

func TestProcessNoWorkflow(t *testing.T) {
	proc, _ := newTestProc()
	_, ok := proc.managers[components.CategoryWorkflow]
	require.False(t, ok, "workflow cannot be registered as user facing component")
}

func TestReporter(t *testing.T) {
	pubsubComponent := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub",
		},
		Spec: componentsapi.ComponentSpec{
			Type:        "pubsub.mockPubSub",
			Version:     "v1",
			Metadata:    daprt.GetFakeMetadataItems(),
			InitTimeout: "2",
		},
	}

	t.Run("event is reported on successful Init",
		func(t *testing.T) {
			resultChan := make(chan *operatorv1.ResourceResult, 1)
			proc, reg := newTestProc(
				withReporter(func(_ context.Context, _ componentsapi.Component, result *operatorv1.ResourceResult) error {
					resultChan <- result
					return nil
				}))

			mockPubSub := new(daprt.MockPubSub)
			reg.PubSubs().RegisterComponent(
				func(_ logger.Logger) pubsub.PubSub {
					return mockPubSub
				},
				"mockPubSub",
			)

			mockPubSub.On("Init", mock.Anything).Return(nil)
			mockPubSub.On("Close").Return(nil)

			err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
			require.NoError(t, err)

			select {
			case result := <-resultChan:
				assert.Equal(t, operatorv1.ResourceType_RESOURCE_COMPONENT, result.GetResourceType())
				assert.Equal(t, operatorv1.EventType_EVENT_INIT, result.GetEventType())
				assert.Equal(t, operatorv1.ResourceConditionStatus_STATUS_SUCCESS, result.GetCondition())
				assert.Equal(t, pubsubComponent.Name, result.GetName())
			case <-time.After(5 * time.Second):
				t.Error("Timed out waiting for reporter result")
			}

			err = proc.Close(pubsubComponent)
			require.NoError(t, err)
		})

	t.Run("event is reported on failed Init",
		func(t *testing.T) {
			resultChan := make(chan *operatorv1.ResourceResult, 1)
			proc, reg := newTestProc(
				withReporter(func(_ context.Context, _ componentsapi.Component, result *operatorv1.ResourceResult) error {
					resultChan <- result
					return nil
				}))

			mockPubSub := new(daprt.MockPubSub)
			reg.PubSubs().RegisterComponent(
				func(_ logger.Logger) pubsub.PubSub {
					return mockPubSub
				},
				"mockPubSub",
			)

			mockPubSub.On("Init", mock.Anything, mock.Anything).Return(errors.New("error"))

			err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
			require.Error(t, err)

			select {
			case result := <-resultChan:
				assert.Equal(t, operatorv1.ResourceType_RESOURCE_COMPONENT, result.GetResourceType())
				assert.Equal(t, operatorv1.EventType_EVENT_INIT, result.GetEventType())
				assert.Equal(t, operatorv1.ResourceConditionStatus_STATUS_FAILURE, result.GetCondition())
				assert.Equal(t, pubsubComponent.Name, result.GetName())
			case <-time.After(5 * time.Second):
				t.Error("Timed out waiting for reporter result")
			}

			err = proc.Close(pubsubComponent)
			require.NoError(t, err)
		})

	t.Run("event is reported on successful Close",
		func(t *testing.T) {
			resultChan := make(chan *operatorv1.ResourceResult, 1)
			proc, reg := newTestProc(
				withReporter(func(_ context.Context, _ componentsapi.Component, result *operatorv1.ResourceResult) error {
					resultChan <- result
					return nil
				}))

			mockPubSub := new(daprt.MockPubSub)
			reg.PubSubs().RegisterComponent(
				func(_ logger.Logger) pubsub.PubSub {
					return mockPubSub
				},
				"mockPubSub",
			)

			mockPubSub.On("Init", mock.Anything, mock.Anything).Return(nil)
			mockPubSub.On("Close").Return(nil)

			err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
			require.NoError(t, err)

			// consume the init message
			<-resultChan

			err = proc.Close(pubsubComponent)
			require.NoError(t, err)

			select {
			case result := <-resultChan:
				assert.Equal(t, operatorv1.ResourceType_RESOURCE_COMPONENT, result.GetResourceType())
				assert.Equal(t, operatorv1.EventType_EVENT_CLOSE, result.GetEventType())
				assert.Equal(t, operatorv1.ResourceConditionStatus_STATUS_SUCCESS, result.GetCondition())
				assert.Equal(t, pubsubComponent.Name, result.GetName())
			case <-time.After(5 * time.Second):
				t.Error("Timed out waiting for reporter result")
			}
		})

	t.Run("event is reported on failed Close",
		func(t *testing.T) {
			resultChan := make(chan *operatorv1.ResourceResult, 1)
			proc, reg := newTestProc(
				withReporter(func(_ context.Context, _ componentsapi.Component, result *operatorv1.ResourceResult) error {
					resultChan <- result
					return nil
				}))

			mockPubSub := new(daprt.MockPubSub)
			reg.PubSubs().RegisterComponent(
				func(_ logger.Logger) pubsub.PubSub {
					return mockPubSub
				},
				"mockPubSub",
			)

			mockPubSub.On("Init", mock.Anything, mock.Anything).Return(nil)
			mockPubSub.On("Close").Return(errors.New("error"))

			err := proc.processComponentAndDependents(context.Background(), pubsubComponent)
			require.NoError(t, err)

			// consume the init message
			<-resultChan

			err = proc.Close(pubsubComponent)
			require.Error(t, err)

			select {
			case result := <-resultChan:
				assert.Equal(t, operatorv1.ResourceType_RESOURCE_COMPONENT, result.GetResourceType())
				assert.Equal(t, operatorv1.EventType_EVENT_CLOSE, result.GetEventType())
				assert.Equal(t, operatorv1.ResourceConditionStatus_STATUS_FAILURE, result.GetCondition())
				assert.Equal(t, pubsubComponent.Name, result.GetName())
			case <-time.After(5 * time.Second):
				t.Error("Timed out waiting for reporter result")
			}
		})
}

func TestProcessorWaitGroupError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for processor to return")
		}
	})
	proc, _ := newTestProc()
	// spin up the processor
	go func() {
		errCh <- proc.Process(ctx)
	}()

	comp1 := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub1",
		},
		Spec: componentsapi.ComponentSpec{
			Type:         "pubsub.mockPubSub",
			Version:      "v1",
			Metadata:     daprt.GetFakeMetadataItems(),
			InitTimeout:  "2",
			IgnoreErrors: true,
		},
	}
	comp2 := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testpubsub2",
		},
		Spec: componentsapi.ComponentSpec{
			Type:         "pubsub.mockPubSub",
			Version:      "v1",
			Metadata:     daprt.GetFakeMetadataItems(),
			InitTimeout:  "2",
			IgnoreErrors: true,
		},
	}

	var wg sync.WaitGroup
	wg.Add(10_000 * 2)

	for range 10_000 {
		go func() {
			if proc.AddPendingComponent(ctx, comp1) {
				proc.WaitForEmptyComponentQueue()
				wg.Done()
			}
		}()
		go func() {
			if proc.AddPendingComponent(ctx, comp2) {
				proc.WaitForEmptyComponentQueue()
				wg.Done()
			}
		}()
	}

	wg.Wait()
}
