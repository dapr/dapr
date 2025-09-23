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
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
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
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/lock"
	mdata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/apphealth"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/metrics"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	"github.com/dapr/dapr/pkg/security"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	pb "github.com/dapr/dapr/pkg/api/grpc/proxy/testservice"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	modeconfig "github.com/dapr/dapr/pkg/config/modes"
	"github.com/dapr/dapr/pkg/cors"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/processor"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/crypto/spiffe"
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

func TestNewRuntime(t *testing.T) {
	// act
	r, err := newDaprRuntime(t.Context(), nil, &internalConfig{
		mode: modes.StandaloneMode,
		metricsExporter: metrics.New(metrics.Options{
			Log:       log,
			Namespace: metrics.DefaultMetricNamespace,
			Healthz:   healthz.New(),
		}),
		registry:         registry.New(registry.NewOptions()),
		healthz:          healthz.New(),
		schedulerStreams: 3,
	}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))

	// assert
	require.NoError(t, err)
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestDoProcessComponent(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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
		mockLockStore.EXPECT().InitLockStore(t.Context(), gomock.Any()).Return(assert.AnError)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		// act
		err := rt.processor.Init(t.Context(), lockComponent)

		// assert
		require.Error(t, err, "expected an error")
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
		err := rt.processor.Init(t.Context(), lockComponentV3)

		// assert
		require.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.CreateComponentFailure, "testlock (lock.mockLock/v3)", errors.New("couldn't find lock store lock.mockLock/v3")).Error())
	})

	t.Run("test error when lock prefix strategy invalid", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(t.Context(), gomock.Any()).Return(nil)

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
		err := rt.processor.Init(t.Context(), lockComponentWithWrongStrategy)
		// assert
		require.Error(t, err)
	})

	t.Run("lock init successfully and set right strategy", func(t *testing.T) {
		// setup
		ctrl := gomock.NewController(t)
		mockLockStore := daprt.NewMockStore(ctrl)
		mockLockStore.EXPECT().InitLockStore(t.Context(), gomock.Any()).Return(nil)

		rt.runtimeConfig.registry.Locks().RegisterComponent(
			func(_ logger.Logger) lock.Store {
				return mockLockStore
			},
			"mockLock",
		)

		// act
		err := rt.processor.Init(t.Context(), lockComponent)
		// assert
		require.NoError(t, err, "unexpected error")
		// get modified key
		key, err := lockLoader.GetModifiedLockKey("test", "mockLock", "appid-1")
		require.NoError(t, err, "unexpected error")
		assert.Equal(t, "lock||appid-1||test", key)
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
		err := rt.processor.Init(t.Context(), pubsubComponent)

		// assert
		require.Error(t, err, "expected an error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "testpubsub (pubsub.mockPubSub/v1)", assert.AnError).Error(), "expected error strings to match")
	})

	t.Run("test invalid category component", func(t *testing.T) {
		// act
		err := rt.processor.Init(t.Context(), componentsV1alpha1.Component{
			Spec: componentsV1alpha1.ComponentSpec{
				Type: "invalid",
			},
		})
		// assert
		require.Error(t, err, "error expected")
	})
}

// Test that flushOutstandingComponents waits for components.
func TestFlushOutstandingComponent(t *testing.T) {
	t.Run("We can call flushOustandingComponents more than once", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		wasCalled := false
		m := rtmock.NewMockKubernetesStoreWithInitCallback(func(context.Context) error {
			time.Sleep(100 * time.Millisecond)
			wasCalled = true
			return nil
		})
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		go rt.processor.Process(t.Context())
		rt.processor.AddPendingComponent(t.Context(), componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		rt.flushOutstandingComponents(t.Context())
		assert.True(t, wasCalled)

		// Make sure that the goroutine was restarted and can flush a second time
		wasCalled = false
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock2",
		)

		rt.processor.AddPendingComponent(t.Context(), componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock2",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		rt.flushOutstandingComponents(t.Context())
		assert.True(t, wasCalled)
	})
	t.Run("flushOutstandingComponents blocks for components with outstanding dependanices", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)
		defer stopRuntime(t, rt)
		wasCalled := false
		wasCalledChild := false
		wasCalledGrandChild := false
		m := rtmock.NewMockKubernetesStoreWithInitCallback(func(context.Context) error {
			time.Sleep(100 * time.Millisecond)
			wasCalled = true
			return nil
		})
		mc := rtmock.NewMockKubernetesStoreWithInitCallback(func(context.Context) error {
			time.Sleep(100 * time.Millisecond)
			wasCalledChild = true
			return nil
		})
		mgc := rtmock.NewMockKubernetesStoreWithInitCallback(func(context.Context) error {
			time.Sleep(100 * time.Millisecond)
			wasCalledGrandChild = true
			return nil
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

		go rt.processor.Process(t.Context())
		rt.processor.AddPendingComponent(t.Context(), componentsV1alpha1.Component{
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
		})
		rt.processor.AddPendingComponent(t.Context(), componentsV1alpha1.Component{
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
		})
		rt.processor.AddPendingComponent(t.Context(), componentsV1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:    "secretstores.kubernetesMock",
				Version: "v1",
			},
		})
		rt.flushOutstandingComponents(t.Context())
		assert.True(t, wasCalled)
		assert.True(t, wasCalledChild)
		assert.True(t, wasCalledGrandChild)
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

		expectedMetadata := nameresolution.Metadata{
			Base: mdata.Base{
				Name: resolverName,
			},
			Instance: nameresolution.Instance{
				DaprHTTPPort:     rt.runtimeConfig.httpPort,
				DaprInternalPort: rt.runtimeConfig.internalGRPCPort,
				AppPort:          rt.runtimeConfig.appConnectionConfig.Port,
				Address:          rt.hostAddress,
				AppID:            rt.runtimeConfig.id,
				Namespace:        "default",
			},
		}

		mockResolver.On("Init", expectedMetadata).Return(e)

		return mockResolver
	}

	t.Run("error on unknown resolver", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "targetResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "anotherResolver", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.Error(t, err)
	})

	t.Run("test init nameresolution", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{
			Component: "someResolver",
		}

		// registered resolver
		initMockResolverForRuntime(rt, "someResolver", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in StandaloneMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in StandaloneMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "mdns", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution default in KubernetesMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.KubernetesMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = &config.NameResolutionSpec{}

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init nameresolution nil in KubernetesMode", func(t *testing.T) {
		// given
		rt, err := NewTestDaprRuntime(t, modes.KubernetesMode)
		require.NoError(t, err)

		// target resolver
		rt.globalConfig.Spec.NameResolutionSpec = nil

		// registered resolver
		initMockResolverForRuntime(rt, "kubernetes", nil)

		// act
		err = rt.initNameResolution(t.Context())

		// assert
		require.NoError(t, err, "expected no error")
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
				Headers:         "header1=value1,header2=value2",
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
		name: "invalid otel trace exporter headers",
		tracingConfig: config.TracingSpec{
			Otel: &config.OtelSpec{
				EndpointAddress: "foo.bar",
				IsSecure:        ptr.Of(false),
				Protocol:        "http",
				Headers:         "invalidheaders",
			},
		},
		expectedErr: "invalid headers provided for Otel endpoint",
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

	for i, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
			require.NoError(t, err)
			defer stopRuntime(t, rt)
			rt.globalConfig.Spec.TracingSpec = &testcases[i].tracingConfig
			if tc.hostAddress != "" {
				rt.hostAddress = tc.hostAddress
			}
			// Setup tracing with the fake tracer provider  store to confirm
			// the right exporter was registered.
			tpStore := newFakeTracerProviderStore()
			if err := rt.setupTracing(t.Context(), rt.hostAddress, tpStore); tc.expectedErr != "" {
				assert.Contains(t, err.Error(), tc.expectedErr)
			} else {
				require.NoError(t, err)
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
				rt.setupTracing(t.Context(), rt.hostAddress, newOpentelemetryTracerProviderStore())
			}
		})
	}
}

func TestPopulateSecretsConfiguration(t *testing.T) {
	t.Run("secret store configuration is populated", func(t *testing.T) {
		// setup
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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

// Test InitSecretStore if secretstore.* refers to Kubernetes secret store.
func TestInitSecretStoresInKubernetesMode(t *testing.T) {
	t.Run("built-in secret store is added", func(t *testing.T) {
		rt, _ := NewTestDaprRuntime(t, modes.KubernetesMode)

		m := rtmock.NewMockKubernetesStore()
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			secretstoresLoader.BuiltinKubernetesSecretStore,
		)

		assertBuiltInSecretStore(t, rt)
	})

	t.Run("disable built-in secret store flag", func(t *testing.T) {
		rt, _ := NewTestDaprRuntime(t, modes.KubernetesMode)
		defer stopRuntime(t, rt)
		rt.runtimeConfig.disableBuiltinK8sSecretStore = true

		testOk := make(chan struct{})
		defer close(testOk)
		go func() {
			// If the test fails, this call blocks forever, eventually causing a timeout
			rt.appendBuiltinSecretStore(t.Context())
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
		rt, _ := NewTestDaprRuntime(t, modes.KubernetesMode)
		rt.authz = rt.authz.WithComponentAuthorizers([]authorizer.ComponentAuthorizer{
			func(component componentsV1alpha1.Component) bool {
				return false
			},
		})

		m := rtmock.NewMockKubernetesStore()
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
	go rt.processor.Process(t.Context())
	rt.appendBuiltinSecretStore(t.Context())
	assert.Eventually(t, func() bool {
		_, ok := rt.compStore.GetComponent(secretstoresLoader.BuiltinKubernetesSecretStore)
		return ok
	}, time.Second*2, time.Millisecond*100)

	require.NoError(t, rt.runnerCloser.Close())
}

func NewTestDaprRuntime(t *testing.T, mode modes.DaprMode) (*DaprRuntime, error) {
	return NewTestDaprRuntimeWithProtocol(t, mode, string(protocol.HTTPProtocol), 1024)
}

func NewTestDaprRuntimeWithID(t *testing.T, mode modes.DaprMode, id string) (*DaprRuntime, error) {
	testRuntimeConfig := NewTestDaprRuntimeConfig(t, modes.StandaloneMode, string(protocol.HTTPProtocol), 1024)
	testRuntimeConfig.id = id
	rt, err := newDaprRuntime(t.Context(), testSecurity(t), testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	if err != nil {
		return nil, err
	}
	rt.runtimeConfig.mode = mode
	rt.channels.Refresh()
	rt.wfengine = wfenginefake.New()
	return rt, nil
}

func NewTestDaprRuntimeWithProtocol(t *testing.T, mode modes.DaprMode, protocol string, appPort int) (*DaprRuntime, error) {
	testRuntimeConfig := NewTestDaprRuntimeConfig(t, modes.StandaloneMode, protocol, appPort)
	rt, err := newDaprRuntime(t.Context(), testSecurity(t), testRuntimeConfig, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	if err != nil {
		return nil, err
	}
	rt.runtimeConfig.mode = mode
	rt.actors = actorsfake.New()
	rt.channels.Refresh()
	return rt, nil
}

func NewTestDaprRuntimeConfig(t *testing.T, mode modes.DaprMode, appProtocol string, appPort int) *internalConfig {
	return &internalConfig{
		id:            daprt.TestRuntimeConfigID,
		actorsService: "placement:10.10.10.12",
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
		apiGRPCPort:                  0,
		apiListenAddresses:           []string{DefaultAPIListenAddress},
		publicPort:                   nil,
		profilePort:                  DefaultProfilePort,
		enableProfiling:              false,
		mTLSEnabled:                  false,
		sentryServiceAddress:         "",
		maxRequestBodySize:           4 << 20,
		readBufferSize:               4 << 10,
		unixDomainSocket:             "",
		gracefulShutdownDuration:     time.Second,
		enableAPILogging:             ptr.Of(true),
		schedulerStreams:             3,
		disableBuiltinK8sSecretStore: false,
		metricsExporter: metrics.New(metrics.Options{
			Log:       log,
			Namespace: metrics.DefaultMetricNamespace,
			Healthz:   healthz.New(),
		}),
		healthz:         healthz.New(),
		outboundHealthz: healthz.New(),
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
	r, err := NewTestDaprRuntime(t, modes.StandaloneMode)
	require.NoError(t, err)
	assert.Equal(t, time.Second, r.runtimeConfig.gracefulShutdownDuration)
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

func TestInitActors(t *testing.T) {
	t.Run("missing namespace on kubernetes", func(t *testing.T) {
		r, err := NewTestDaprRuntime(t, modes.KubernetesMode)
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.namespace = ""
		r.runtimeConfig.mTLSEnabled = true

		err = r.initActors(t.Context())
		require.Error(t, err)
	})

	t.Run("actors hosted = true", func(t *testing.T) {
		r, err := NewTestDaprRuntime(t, modes.KubernetesMode)
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.appConfig = config.ApplicationConfig{
			Entities: []string{"actor1"},
		}

		hosted := len(r.appConfig.Entities) > 0
		assert.True(t, hosted)
	})

	t.Run("actors hosted = false", func(t *testing.T) {
		r, err := NewTestDaprRuntime(t, modes.KubernetesMode)
		require.NoError(t, err)
		defer stopRuntime(t, r)

		hosted := len(r.appConfig.Entities) > 0
		assert.False(t, hosted)
	})

	t.Run("placement enable = false", func(t *testing.T) {
		r, err := newDaprRuntime(t.Context(), testSecurity(t), &internalConfig{
			schedulerStreams: 3,
			metricsExporter: metrics.New(metrics.Options{
				Log:       log,
				Namespace: metrics.DefaultMetricNamespace,
				Healthz:   healthz.New(),
			}),
			mode:     modes.StandaloneMode,
			registry: registry.New(registry.NewOptions()),
			healthz:  healthz.New(),
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.channels.Refresh()

		err = r.initActors(t.Context())
		require.NoError(t, err)
	})

	t.Run("the state stores can still be initialized normally", func(t *testing.T) {
		r, err := newDaprRuntime(t.Context(), testSecurity(t), &internalConfig{
			metricsExporter: metrics.New(metrics.Options{
				Log:       log,
				Namespace: metrics.DefaultMetricNamespace,
				Healthz:   healthz.New(),
			}),
			mode:             modes.StandaloneMode,
			registry:         registry.New(registry.NewOptions()),
			healthz:          healthz.New(),
			schedulerStreams: 3,
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.channels.Refresh()

		assert.NotNil(t, r.compStore.ListStateStores())
		assert.Equal(t, 0, r.compStore.StateStoresLen())
	})

	t.Run("the actor store can not be initialized normally", func(t *testing.T) {
		r, err := newDaprRuntime(t.Context(), testSecurity(t), &internalConfig{
			metricsExporter: metrics.New(metrics.Options{
				Log:       log,
				Namespace: metrics.DefaultMetricNamespace,
				Healthz:   healthz.New(),
			}),
			mode:             modes.StandaloneMode,
			registry:         registry.New(registry.NewOptions()),
			healthz:          healthz.New(),
			schedulerStreams: 3,
		}, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
		require.NoError(t, err)
		defer stopRuntime(t, r)
		r.channels.Refresh()

		name, ok := r.processor.State().ActorStateStoreName()
		assert.False(t, ok)
		assert.Equal(t, "", name)
		err = r.initActors(t.Context())
		require.NoError(t, err)
	})
}

func TestActorReentrancyConfig(t *testing.T) {
	fullConfig := `{
		"entities":["actorType1", "actorType2"],
		"actorIdleTimeout": "1h",
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
		"drainOngoingCallTimeout": "30s",
		"drainRebalancedActors": true,
		"reentrancy": {
		  "enabled": true
		}
	  }`

	emptyConfig := `{
		"entities":["actorType1", "actorType2"],
		"actorIdleTimeout": "1h",
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
			r, err := NewTestDaprRuntime(t, modes.StandaloneMode)
			require.NoError(t, err)

			mockAppChannel := new(channelt.MockAppChannel)
			r.channels.WithAppChannel(mockAppChannel)
			r.runtimeConfig.appConnectionConfig.Protocol = protocol.HTTPProtocol

			configResp := config.ApplicationConfig{}
			json.Unmarshal(tc.Config, &configResp)

			mockAppChannel.On("GetAppConfig").Return(&configResp, nil)

			r.loadAppConfiguration(t.Context())

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

func TestCloseWithErrors(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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
			Name: "binding",
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
			Name: "pubsub",
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
			Name: "state",
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
			Name: "secret",
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

	errCh := make(chan error)
	go func() {
		errCh <- rt.Run(t.Context())
	}()

	rt.processor.AddPendingComponent(t.Context(), mockOutputBindingComponent)
	rt.processor.AddPendingComponent(t.Context(), mockPubSubComponent)
	rt.processor.AddPendingComponent(t.Context(), mockStateComponent)
	rt.processor.AddPendingComponent(t.Context(), mockSecretsComponent)

	err = rt.runnerCloser.Close()
	require.Error(t, err)
	assert.Len(t, strings.Split(err.Error(), "\n"), 4)
	select {
	case rErr := <-errCh:
		assert.Equal(t, err, rErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runtime to stop")
	}
}

func stopRuntime(t *testing.T, rt *DaprRuntime) {
	require.NoError(t, rt.runnerCloser.Close())
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
	var callbackInvoked atomic.Bool

	cfg := NewTestDaprRuntimeConfig(t, modes.StandaloneMode, "http", port)
	rt, err := newDaprRuntime(t.Context(), testSecurity(t), cfg, &config.Configuration{}, &config.AccessControlList{}, resiliency.New(logger.NewLogger("test")))
	require.NoError(t, err)
	rt.runtimeConfig.registry = registry.New(registry.NewOptions().WithComponentsCallback(func(components registry.ComponentRegistry) error {
		callbackInvoked.Store(true)
		close(c)
		return nil
	}))

	errCh := make(chan error)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		errCh <- rt.Run(ctx)
	}()

	select {
	case <-c:
	case <-time.After(10 * time.Second):
		assert.Fail(t, "timed out waiting for component callback")
	}

	assert.True(t, callbackInvoked.Load(), "component callback was not invoked")

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for runtime to stop")
	}
}

func TestGRPCProxy(t *testing.T) {
	// setup gRPC server
	serverPort, _ := freeport.GetFreePort()
	teardown, err := runGRPCApp(serverPort)
	require.NoError(t, err)
	defer teardown()

	// setup proxy
	rt, err := NewTestDaprRuntimeWithProtocol(t, modes.StandaloneMode, "grpc", serverPort)
	require.NoError(t, err)
	internalPort, _ := freeport.GetFreePort()
	rt.runtimeConfig.internalGRPCPort = internalPort

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

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() {
		errCh <- rt.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for runtime to stop")
		}
	})

	req := &pb.PingRequest{Value: "foo"}

	t.Run("proxy single streaming request", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
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
		ctx1, cancel := context.WithTimeout(t.Context(), time.Second*5)
		defer cancel()
		stream1, err := pingStreamClient(ctx1, internalPort)
		require.NoError(t, err)

		ctx2, cancel := context.WithTimeout(t.Context(), time.Second)
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

func TestShutdownWithWait(t *testing.T) {
	t.Run("calling ShutdownWithWait should wait until runtime has stopped", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		closeSecretClose := make(chan struct{})
		closeSecretCalled := make(chan struct{})
		m := rtmock.NewMockKubernetesStoreWithClose(func() error {
			close(closeSecretCalled)
			<-closeSecretClose
			return nil
		})
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)
		rt.wfengine = wfenginefake.New()

		dir := t.TempDir()
		rt.runtimeConfig.standalone.ResourcesPath = []string{dir}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kubernetesMock.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: kubernetesMock
spec:
  type: secretstores.kubernetesMock
  version: v1
`), 0o600))

		// Use a background context since this is not closed by the test.
		ctx := t.Context()
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		assert.Eventually(t, func() bool {
			return len(rt.compStore.ListComponents()) > 0
		}, 5*time.Second, 100*time.Millisecond, "timed out waiting for component store to be populated with mock secret")

		shutdownCh := make(chan struct{})
		go func() {
			rt.ShutdownWithWait()
			close(shutdownCh)
		}()

		select {
		case <-closeSecretCalled:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for secret store to be closed")
		}

		select {
		case <-errCh:
			t.Fatal("runtime stopped before ShutdownWithWait returned")
		default:
		}

		select {
		case <-shutdownCh:
			t.Fatal("ShutdownWithWait returned before runtime stopped")
		default:
			close(closeSecretClose)
		}

		select {
		case <-shutdownCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for ShutdownWithWait to return")
		}

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for runtime to stop")
		}
	})

	t.Run("if secret times out after init, error should return from runtime and ShutdownWithWait should return", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		initSecretContextClosed := make(chan struct{})
		closeSecretInit := make(chan struct{})
		m := rtmock.NewMockKubernetesStoreWithInitCallback(func(ctx context.Context) error {
			<-ctx.Done()
			close(initSecretContextClosed)
			<-closeSecretInit
			return nil
		})
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)
		dir := t.TempDir()
		rt.runtimeConfig.standalone.ResourcesPath = []string{dir}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kubernetesMock.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: kubernetesMock
spec:
  type: secretstores.kubernetesMock
  version: v1
  initTimeout: 1ms
`), 0o600))

		// Use a background context since this is not closed by the test.
		ctx := t.Context()
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		select {
		case <-initSecretContextClosed:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for secret store to return inited because of timeout")
		}

		select {
		case <-errCh:
			t.Fatal("runtime returned stopped before secret Close() returned")
		default:
		}

		shutdownCh := make(chan struct{})
		go func() {
			rt.ShutdownWithWait()
			close(shutdownCh)
		}()

		close(closeSecretInit)

		select {
		case <-shutdownCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for ShutdownWithWait to return")
		}

		select {
		case err := <-errCh:
			require.Error(t, err)
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for runtime to stop")
		}

		select {
		case <-shutdownCh:
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for runtime to be marked as stopped")
		}
	})

	t.Run("if secret init fails then the runtime should not error when the error should be ignored. Should wait for shutdown signal", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		secretInited := make(chan struct{})
		m := rtmock.NewMockKubernetesStoreWithInitCallback(func(ctx context.Context) error {
			close(secretInited)
			return errors.New("this is an error")
		})

		secretClosed := make(chan struct{})
		m.(*rtmock.MockKubernetesStateStore).CloseFn = func() error {
			close(secretClosed)
			return nil
		}
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)
		rt.wfengine = wfenginefake.New()

		dir := t.TempDir()
		rt.runtimeConfig.standalone.ResourcesPath = []string{dir}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kubernetesMock.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: kubernetesMock
spec:
  type: secretstores.kubernetesMock
  version: v1
  ignoreErrors: true
`), 0o600))

		// Use a background context since this is not closed by the test.
		ctx := t.Context()
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		select {
		case <-secretInited:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for secret store to be inited")
		}

		shutdownCh := make(chan struct{})
		go func() {
			rt.ShutdownWithWait()
			close(shutdownCh)
		}()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for runtime to stop")
		}

		select {
		case <-shutdownCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for ShutdownWithWait to return")
		}

		select {
		case <-secretClosed:
			t.Fatal("secret store closed should not be called when init failed")
		default:
		}
	})
	t.Run("if secret init fails then the runtime should error when the error should NOT be ignored. Shouldn't wait for shutdown signal", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		m := rtmock.NewMockKubernetesStoreWithInitCallback(func(ctx context.Context) error {
			return errors.New("this is an error")
		})

		secretClosed := make(chan struct{})
		m.(*rtmock.MockKubernetesStateStore).CloseFn = func() error {
			close(secretClosed)
			return nil
		}
		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		dir := t.TempDir()
		rt.runtimeConfig.standalone.ResourcesPath = []string{dir}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kubernetesMock.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: kubernetesMock
spec:
  type: secretstores.kubernetesMock
  version: v1
`), 0o600))

		// Use a background context since this is not closed by the test.
		ctx := t.Context()
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		select {
		case err := <-errCh:
			require.ErrorContains(t, err, "this is an error")
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for runtime to error")
		}

		select {
		case <-secretClosed:
			t.Fatal("secret store should not be closed when init failed")
		default:
		}

		// ShutdownWithWait() can still be called even if the runtime errored, it
		// will just return immediately.
		shutdownCh := make(chan struct{})
		go func() {
			rt.ShutdownWithWait()
			close(shutdownCh)
		}()

		select {
		case <-shutdownCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for ShutdownWithWait to return")
		}
	})

	t.Run("runtime should fatal if closing components does not happen in time", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		m := rtmock.NewMockKubernetesStoreWithClose(func() error {
			<-time.After(5 * time.Second)
			return nil
		})
		rt.runtimeConfig.gracefulShutdownDuration = time.Millisecond * 10

		fatalShutdownCalled := make(chan struct{})
		rt.runnerCloser.WithFatalShutdown(func() {
			close(fatalShutdownCalled)
		})

		rt.runtimeConfig.registry.SecretStores().RegisterComponent(
			func(_ logger.Logger) secretstores.SecretStore {
				return m
			},
			"kubernetesMock",
		)

		dir := t.TempDir()
		rt.runtimeConfig.standalone.ResourcesPath = []string{dir}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "kubernetesMock.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: kubernetesMock
spec:
  type: secretstores.kubernetesMock
  version: v1
`), 0o600))

		// Use a background context since this is not closed by the test.
		ctx := t.Context()
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		assert.Eventually(t, func() bool {
			return len(rt.compStore.ListSecretStores()) > 0
		}, 5*time.Second, 100*time.Millisecond, "secret store not init in time")

		go rt.ShutdownWithWait()

		select {
		case <-fatalShutdownCalled:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for fatal shutdown to return")
		}
	})
}

func TestGetComponentsCapabilitiesMap(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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

	require.NoError(t, rt.processor.Init(t.Context(), cin))
	require.NoError(t, rt.processor.Init(t.Context(), cout))
	require.NoError(t, rt.processor.Init(t.Context(), cPubSub))
	require.NoError(t, rt.processor.Init(t.Context(), cStateStore))
	require.NoError(t, rt.processor.Init(t.Context(), cSecretStore))

	capabilities := rt.getComponentsCapabilitesMap()
	assert.Len(t, capabilities, 5,
		"All 5 registered components have are present in capabilities (stateStore pubSub input output secretStore)")
	assert.Len(t, capabilities["mockPubSub"], 2,
		"mockPubSub has 2 features because we mocked it so")
	assert.Len(t, capabilities["testInputBinding"], 1,
		"Input bindings always have INPUT_BINDING added to their capabilities")
	assert.Len(t, capabilities["testOutputBinding"], 1,
		"Output bindings always have OUTPUT_BINDING added to their capabilities")
	assert.Len(t, capabilities[mockSecretStoreName], 1,
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
	//nolint:staticcheck
	clientConn, err := grpc.DialContext(
		ctx,
		fmt.Sprintf("localhost:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
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
		pong := &pb.PingResponse{Value: ping.GetValue(), Counter: counter}
		if err := stream.Send(pong); err != nil {
			return err
		}
		counter++
	}
	return nil
}

func matchDaprRequestMethod(method string) any {
	return mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
		if req == nil || req.Message() == nil || req.Message().GetMethod() != method {
			return false
		}
		return true
	})
}

func TestGracefulShutdownBindings(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() {
		errCh <- rt.Run(ctx)
	}()

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
	require.NoError(t, rt.processor.Init(t.Context(), cin))
	require.NoError(t, rt.processor.Init(t.Context(), cout))
	assert.Len(t, rt.compStore.ListInputBindings(), 1)
	assert.Len(t, rt.compStore.ListOutputBindings(), 1)

	cancel()
	select {
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "input bindings shutdown timed out")
	case err := <-errCh:
		require.NoError(t, err)
	}
}

func TestBlockShutdownBindings(t *testing.T) {
	t.Run("block timeout", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakeClock(time.Now())
		rt.clock = fakeClock
		rt.appHealthChanged(t.Context(), apphealth.NewStatus(true, nil))

		rt.runtimeConfig.blockShutdownDuration = ptr.Of(time.Millisecond * 100)
		rt.runtimeConfig.gracefulShutdownDuration = 3 * time.Second

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		cancel()

		select {
		case <-time.After(time.Second):
		case <-errCh:
			assert.Fail(t, "expected not to return until block timeout is reached")
		}

		fakeClock.Step(time.Millisecond * 200)

		select {
		case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
			assert.Fail(t, "input bindings shutdown timed out")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})

	t.Run("block app unhealthy", func(t *testing.T) {
		rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
		require.NoError(t, err)

		fakeClock := clocktesting.NewFakeClock(time.Now())
		rt.clock = fakeClock
		rt.appHealthChanged(t.Context(), apphealth.NewStatus(true, nil))

		rt.runtimeConfig.blockShutdownDuration = ptr.Of(time.Millisecond * 100)
		rt.runtimeConfig.gracefulShutdownDuration = 3 * time.Second

		ctx, cancel := context.WithCancel(t.Context())
		errCh := make(chan error)
		go func() {
			errCh <- rt.Run(ctx)
		}()

		cancel()

		select {
		case <-time.After(time.Second):
		case <-errCh:
			assert.Fail(t, "expected not to return until block timeout is reached")
		}

		rt.appHealthChanged(t.Context(), apphealth.NewStatus(false, nil))

		select {
		case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
			assert.Fail(t, "input bindings shutdown timed out")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})
}

func TestGracefulShutdownPubSub(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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
		ID:             rt.runtimeConfig.id,
		IsHTTP:         rt.runtimeConfig.appConnectionConfig.Protocol.IsHTTP(),
		ActorsEnabled:  len(rt.runtimeConfig.actorsService) > 0,
		Registry:       rt.runtimeConfig.registry,
		ComponentStore: rt.compStore,
		Meta:           rt.meta,
		GlobalConfig:   rt.globalConfig,
		Resiliency:     rt.resiliency,
		Mode:           rt.runtimeConfig.mode,
		Channels:       rt.channels,
		GRPC:           rt.grpc,
		Security:       rt.sec,
	})

	require.NoError(t, rt.processor.Init(t.Context(), cPubSub))

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() {
		errCh <- rt.Run(ctx)
	}()

	rt.appHealthChanged(t.Context(), apphealth.NewStatus(true, nil))

	mockPubSub.AssertCalled(t, "Init", mock.Anything)
	mockPubSub.AssertCalled(t, "Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler"))

	cancel()
	select {
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "pubsub shutdown timed out")
	case err := <-errCh:
		require.NoError(t, err)
	}
}

func TestGracefulShutdownActors(t *testing.T) {
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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

	rt.namespace = "test"
	rt.runtimeConfig.appConnectionConfig.Port = -1

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() {
		errCh <- rt.Run(ctx)
	}()

	select {
	case <-rt.initComplete:
	case <-time.After(time.Second * 5):
		t.Fatal("runtime did not init in time")
	}

	// act
	err = rt.processor.Init(t.Context(), mockStateComponent)

	// assert
	require.NoError(t, err, "expected no error")

	cancel()

	select {
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "actors shutdown timed out")
	case err := <-errCh:
		require.NoError(t, err)
	}

	var activeActCount int32
	runtimeStatus := rt.actors.RuntimeStatus()
	for _, v := range runtimeStatus.GetActiveActors() {
		activeActCount += v.GetCount()
	}
	assert.Equal(t, int32(0), activeActCount)
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
	rt, err := NewTestDaprRuntime(t, modes.StandaloneMode)
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
	require.NoError(t, rt.setupTracing(t.Context(), rt.hostAddress, tpStore))
	assert.NotNil(t, rt.tracerProvider)

	errCh := make(chan error)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		errCh <- rt.Run(ctx)
	}()

	cancel()

	select {
	case <-time.After(rt.runtimeConfig.gracefulShutdownDuration + 2*time.Second):
		assert.Fail(t, "tracing shutdown timed out")
	case err := <-errCh:
		require.NoError(t, err)
	}

	assert.Nil(t, rt.tracerProvider)
}

func testSecurity(t *testing.T) security.Handler {
	secP, err := security.New(t.Context(), security.Options{
		TrustAnchors:            []byte("test"),
		AppID:                   "test",
		ControlPlaneTrustDomain: "test.example.com",
		ControlPlaneNamespace:   "default",
		MTLSEnabled:             false,
		OverrideCertRequestFn: func(context.Context, []byte) (*spiffe.SVIDResponse, error) {
			return nil, nil
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go secP.Run(ctx)
	sec, err := secP.Handler(t.Context())
	require.NoError(t, err)

	return sec
}

func TestGetOtelServiceName(t *testing.T) {
	// Save the original value of the OTEL_SERVICE_NAME variable and restore at the end

	tests := []struct {
		env      string // The value of the OTEL_SERVICE_NAME variable
		fallback string // The fallback value
		expected string // The expected value
	}{
		{"", "my-app", "my-app"},                 // Case 1: No environment variable, use fallback
		{"service-abc", "my-app", "service-abc"}, // Case 2: Environment variable set, use it
	}

	for _, tc := range tests {
		t.Run(tc.env, func(t *testing.T) {
			// Set the environment variable to the test case value
			t.Setenv("OTEL_SERVICE_NAME", tc.env)
			// Call the function and check the result
			got := getOtelServiceName(tc.fallback)
			if got != tc.expected {
				// Report an error if the result doesn't match
				t.Errorf("getOtelServiceName(%q) = %q; expected %q", tc.fallback, got, tc.expected)
			}
		})
	}
}
