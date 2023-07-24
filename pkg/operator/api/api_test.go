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

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/client/clientset/versioned/scheme"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type mockComponentUpdateServer struct {
	grpc.ServerStream
	Calls atomic.Int64
}

func (m *mockComponentUpdateServer) Send(*operatorv1pb.ComponentUpdateEvent) error {
	m.Calls.Add(1)
	return nil
}

func (m *mockComponentUpdateServer) Context() context.Context {
	return context.TODO()
}

type mockHTTPEndpointUpdateServer struct {
	grpc.ServerStream
	Calls atomic.Int64
}

func (m *mockHTTPEndpointUpdateServer) Send(*operatorv1pb.HTTPEndpointUpdateEvent) error {
	m.Calls.Add(1)
	return nil
}

func (m *mockHTTPEndpointUpdateServer) Context() context.Context {
	return context.TODO()
}

func TestProcessComponentSecrets(t *testing.T) {
	t.Run("secret ref exists, not kubernetes secret store, no error", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []commonapi.NameValuePair{
					{
						Name: "test1",
						SecretKeyRef: commonapi.SecretKeyRef{
							Name: "secret1",
							Key:  "key1",
						},
					},
				},
			},
			Auth: componentsapi.Auth{
				SecretStore: "secretstore",
			},
		}

		err := processComponentSecrets(context.Background(), &c, "default", nil)
		assert.NoError(t, err)
	})

	t.Run("secret ref exists, kubernetes secret store, secret extracted", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []commonapi.NameValuePair{
					{
						Name: "test1",
						SecretKeyRef: commonapi.SecretKeyRef{
							Name: "secret1",
							Key:  "key1",
						},
					},
				},
			},
			Auth: componentsapi.Auth{
				SecretStore: kubernetesSecretStore,
			},
		}

		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"key1": []byte("value1"),
				},
			}).
			Build()

		err = processComponentSecrets(context.Background(), &c, "default", client)
		assert.NoError(t, err)

		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, _ := json.Marshal(enc)

		assert.Equal(t, jsonEnc, c.Spec.Metadata[0].Value.Raw)
	})

	t.Run("secret ref exists, default kubernetes secret store, secret extracted", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []commonapi.NameValuePair{
					{
						Name: "test1",
						SecretKeyRef: commonapi.SecretKeyRef{
							Name: "secret1",
							Key:  "key1",
						},
					},
				},
			},
			Auth: componentsapi.Auth{
				SecretStore: "",
			},
		}

		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"key1": []byte("value1"),
				},
			}).
			Build()

		err = processComponentSecrets(context.Background(), &c, "default", client)
		assert.NoError(t, err)

		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, _ := json.Marshal(enc)

		assert.Equal(t, jsonEnc, c.Spec.Metadata[0].Value.Raw)
	})
}

func TestComponentUpdate(t *testing.T) {
	t.Run("skip sidecar update if namespace doesn't match", func(t *testing.T) {
		c := componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns1",
			},
			Spec: componentsapi.ComponentSpec{},
		}

		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).Build()

		mockSidecar := &mockComponentUpdateServer{}
		api := NewAPIServer(client).(*apiServer)

		go func() {
			assert.Eventually(t, func() bool {
				api.connLock.Lock()
				defer api.connLock.Unlock()
				return len(api.allConnUpdateChan) == 1
			}, time.Second, 10*time.Millisecond)

			api.connLock.Lock()
			defer api.connLock.Unlock()
			for key := range api.allConnUpdateChan {
				api.allConnUpdateChan[key] <- &c
				close(api.allConnUpdateChan[key])
			}
		}()

		// Start sidecar update loop
		assert.NoError(t, api.ComponentUpdate(&operatorv1pb.ComponentUpdateRequest{
			Namespace: "ns2",
		}, mockSidecar))

		assert.Equal(t, int64(0), mockSidecar.Calls.Load())
	})

	t.Run("sidecar is updated when component namespace is a match", func(t *testing.T) {
		c := componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns1",
			},
			Spec: componentsapi.ComponentSpec{},
		}

		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).Build()

		mockSidecar := &mockComponentUpdateServer{}
		api := NewAPIServer(client).(*apiServer)

		go func() {
			assert.Eventually(t, func() bool {
				api.connLock.Lock()
				defer api.connLock.Unlock()
				return len(api.allConnUpdateChan) == 1
			}, time.Second, 10*time.Millisecond)

			api.connLock.Lock()
			defer api.connLock.Unlock()
			for key := range api.allConnUpdateChan {
				api.allConnUpdateChan[key] <- &c
				close(api.allConnUpdateChan[key])
			}
		}()

		// Start sidecar update loop
		api.ComponentUpdate(&operatorv1pb.ComponentUpdateRequest{
			Namespace: "ns1",
		}, mockSidecar)

		assert.Equal(t, int64(1), mockSidecar.Calls.Load())
	})
}

func TestHTTPEndpointUpdate(t *testing.T) {
	e := httpendpointapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns1",
		},
		Spec: httpendpointapi.HTTPEndpointSpec{},
	}

	s := runtime.NewScheme()
	err := scheme.AddToScheme(s)
	assert.NoError(t, err)

	err = corev1.AddToScheme(s)
	assert.NoError(t, err)

	client := fake.NewClientBuilder().
		WithScheme(s).Build()

	mockSidecar := &mockHTTPEndpointUpdateServer{}
	api := NewAPIServer(client).(*apiServer)
	t.Run("skip sidecar update if namespace doesn't match", func(t *testing.T) {
		go func() {
			assert.Eventually(t, func() bool {
				api.endpointLock.Lock()
				defer api.endpointLock.Unlock()
				return len(api.allEndpointsUpdateChan) == 1
			}, time.Second, 10*time.Millisecond)

			api.endpointLock.Lock()
			defer api.endpointLock.Unlock()
			for key := range api.allEndpointsUpdateChan {
				api.allEndpointsUpdateChan[key] <- &e
				close(api.allEndpointsUpdateChan[key])
			}
		}()

		// Start sidecar update loop
		assert.NoError(t, api.HTTPEndpointUpdate(&operatorv1pb.HTTPEndpointUpdateRequest{
			Namespace: "ns2",
		}, mockSidecar))

		assert.Equal(t, int64(0), mockSidecar.Calls.Load())
	})

	t.Run("sidecar is updated when endpoint namespace is a match", func(t *testing.T) {
		go func() {
			assert.Eventually(t, func() bool {
				api.endpointLock.Lock()
				defer api.endpointLock.Unlock()
				return len(api.allEndpointsUpdateChan) == 1
			}, time.Second, 10*time.Millisecond)

			api.endpointLock.Lock()
			defer api.endpointLock.Unlock()
			for key := range api.allEndpointsUpdateChan {
				api.allEndpointsUpdateChan[key] <- &e
				close(api.allEndpointsUpdateChan[key])
			}
		}()

		// Start sidecar update loop
		assert.NoError(t, api.HTTPEndpointUpdate(&operatorv1pb.HTTPEndpointUpdateRequest{
			Namespace: "ns1",
		}, mockSidecar))

		assert.Equal(t, int64(1), mockSidecar.Calls.Load())
	})
}

func TestListsNamespaced(t *testing.T) {
	t.Run("list components namespace scoping", func(t *testing.T) {
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = componentsapi.AddToScheme(s)
		assert.NoError(t, err)

		av, kind := componentsapi.SchemeGroupVersion.WithKind("Component").ToAPIVersionAndKind()
		typeMeta := metav1.TypeMeta{
			Kind:       kind,
			APIVersion: av,
		}
		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&componentsapi.Component{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj1",
					Namespace: "namespace-a",
				},
			}, &componentsapi.Component{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj2",
					Namespace: "namespace-b",
				},
			}).
			Build()

		api := NewAPIServer(client).(*apiServer)

		res, err := api.ListComponents(context.TODO(), &operatorv1pb.ListComponentsRequest{
			PodName:   "foo",
			Namespace: "namespace-a",
		})
		assert.NoError(t, err)
		assert.Equal(t, 1, len(res.GetComponents()))

		var sub resiliencyapi.Resiliency
		assert.NoError(t, yaml.Unmarshal(res.GetComponents()[0], &sub))

		assert.Equal(t, "obj1", sub.Name)
		assert.Equal(t, "namespace-a", sub.Namespace)

		res, err = api.ListComponents(context.TODO(), &operatorv1pb.ListComponentsRequest{
			PodName:   "foo",
			Namespace: "namespace-c",
		})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(res.GetComponents()))
	})
	t.Run("list subscriptions namespace scoping", func(t *testing.T) {
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = subscriptionsapiV2alpha1.AddToScheme(s)
		assert.NoError(t, err)

		av, kind := subscriptionsapiV2alpha1.SchemeGroupVersion.WithKind("Subscription").ToAPIVersionAndKind()
		typeMeta := metav1.TypeMeta{
			Kind:       kind,
			APIVersion: av,
		}
		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&subscriptionsapiV2alpha1.Subscription{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sub1",
					Namespace: "namespace-a",
				},
			}, &subscriptionsapiV2alpha1.Subscription{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sub2",
					Namespace: "namespace-b",
				},
			}).
			Build()

		api := NewAPIServer(client).(*apiServer)

		res, err := api.ListSubscriptionsV2(context.TODO(), &operatorv1pb.ListSubscriptionsRequest{
			PodName:   "foo",
			Namespace: "namespace-a",
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, len(res.GetSubscriptions()))

		var sub subscriptionsapiV2alpha1.Subscription
		err = yaml.Unmarshal(res.GetSubscriptions()[0], &sub)
		assert.Nil(t, err)

		assert.Equal(t, "sub1", sub.Name)
		assert.Equal(t, "namespace-a", sub.Namespace)

		res, err = api.ListSubscriptionsV2(context.TODO(), &operatorv1pb.ListSubscriptionsRequest{
			PodName:   "baz",
			Namespace: "namespace-c",
		})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(res.GetSubscriptions()))
	})
	t.Run("list resiliencies namespace scoping", func(t *testing.T) {
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = resiliencyapi.AddToScheme(s)
		assert.NoError(t, err)

		av, kind := resiliencyapi.SchemeGroupVersion.WithKind("Resiliency").ToAPIVersionAndKind()
		typeMeta := metav1.TypeMeta{
			Kind:       kind,
			APIVersion: av,
		}
		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&resiliencyapi.Resiliency{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj1",
					Namespace: "namespace-a",
				},
			}, &resiliencyapi.Resiliency{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj2",
					Namespace: "namespace-b",
				},
			}).
			Build()

		api := NewAPIServer(client).(*apiServer)

		res, err := api.ListResiliency(context.TODO(), &operatorv1pb.ListResiliencyRequest{
			Namespace: "namespace-a",
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, len(res.GetResiliencies()))

		var sub resiliencyapi.Resiliency
		err = yaml.Unmarshal(res.GetResiliencies()[0], &sub)
		assert.Nil(t, err)

		assert.Equal(t, "obj1", sub.Name)
		assert.Equal(t, "namespace-a", sub.Namespace)

		res, err = api.ListResiliency(context.TODO(), &operatorv1pb.ListResiliencyRequest{
			Namespace: "namespace-c",
		})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(res.GetResiliencies()))
	})
	t.Run("list http endpoints namespace scoping", func(t *testing.T) {
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = httpendpointapi.AddToScheme(s)
		assert.NoError(t, err)

		av, kind := httpendpointapi.SchemeGroupVersion.WithKind("HTTPEndpoint").ToAPIVersionAndKind()
		typeMeta := metav1.TypeMeta{
			Kind:       kind,
			APIVersion: av,
		}
		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&httpendpointapi.HTTPEndpoint{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj1",
					Namespace: "namespace-a",
				},
			}, &httpendpointapi.HTTPEndpoint{
				TypeMeta: typeMeta,
				ObjectMeta: metav1.ObjectMeta{
					Name:      "obj2",
					Namespace: "namespace-b",
				},
			}).
			Build()

		api := NewAPIServer(client).(*apiServer)

		res, err := api.ListHTTPEndpoints(context.TODO(), &operatorv1pb.ListHTTPEndpointsRequest{
			Namespace: "namespace-a",
		})

		assert.Nil(t, err)
		assert.Equal(t, 1, len(res.GetHttpEndpoints()))

		var endpoint httpendpointapi.HTTPEndpoint
		err = yaml.Unmarshal(res.GetHttpEndpoints()[0], &endpoint)
		assert.Nil(t, err)

		assert.Equal(t, "obj1", endpoint.Name)
		assert.Equal(t, "namespace-a", endpoint.Namespace)

		res, err = api.ListHTTPEndpoints(context.TODO(), &operatorv1pb.ListHTTPEndpointsRequest{
			Namespace: "namespace-c",
		})
		assert.Nil(t, err)
		assert.Equal(t, 0, len(res.GetHttpEndpoints()))
	})
}

func TestProcessHTTPEndpointSecrets(t *testing.T) {
	e := httpendpointapi.HTTPEndpoint{
		Spec: httpendpointapi.HTTPEndpointSpec{
			BaseURL: "http://test.com/",
			Headers: []commonapi.NameValuePair{
				{
					Name: "test1",
					SecretKeyRef: commonapi.SecretKeyRef{
						Name: "secret1",
						Key:  "key1",
					},
				},
			},
		},
		Auth: httpendpointapi.Auth{
			SecretStore: "secretstore",
		},
	}
	t.Run("secret ref exists, not kubernetes secret store, no error", func(t *testing.T) {
		err := processHTTPEndpointSecrets(context.Background(), &e, "default", nil)
		assert.NoError(t, err)
	})

	t.Run("secret ref exists, kubernetes secret store, secret extracted", func(t *testing.T) {
		e.Auth.SecretStore = kubernetesSecretStore
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"key1": []byte("value1"),
				},
			}).
			Build()
		assert.NoError(t, processHTTPEndpointSecrets(context.Background(), &e, "default", client))
		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, err := json.Marshal(enc)
		assert.NoError(t, err)
		assert.Equal(t, jsonEnc, e.Spec.Headers[0].Value.Raw)
	})

	t.Run("secret ref exists, default kubernetes secret store, secret extracted", func(t *testing.T) {
		e.Auth.SecretStore = ""
		s := runtime.NewScheme()
		err := scheme.AddToScheme(s)
		assert.NoError(t, err)

		err = corev1.AddToScheme(s)
		assert.NoError(t, err)

		client := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secret1",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"key1": []byte("value1"),
				},
			}).
			Build()

		assert.NoError(t, processHTTPEndpointSecrets(context.Background(), &e, "default", client))

		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, err := json.Marshal(enc)
		assert.NoError(t, err)
		assert.Equal(t, jsonEnc, e.Spec.Headers[0].Value.Raw)
	})
}

func Test_Ready(t *testing.T) {
	tests := map[string]struct {
		readyCh func() chan struct{}
		ctx     func() context.Context
		expErr  bool
	}{
		"if readyCh is closed, then expect no error": {
			readyCh: func() chan struct{} {
				ch := make(chan struct{})
				close(ch)
				return ch
			},
			ctx:    context.Background,
			expErr: false,
		},
		"if context is cancelled, then expect error": {
			readyCh: func() chan struct{} {
				ch := make(chan struct{})
				return ch
			},
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			expErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			err := (&apiServer{readyCh: test.readyCh()}).Ready(test.ctx())
			assert.Equal(t, test.expErr, err != nil, err)
		})
	}
}
