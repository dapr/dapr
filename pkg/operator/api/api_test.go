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

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
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

func TestProcessComponentSecrets(t *testing.T) {
	t.Run("secret ref exists, not kubernetes secret store, no error", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []componentsapi.MetadataItem{
					{
						Name: "test1",
						SecretKeyRef: componentsapi.SecretKeyRef{
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

		err := processComponentSecrets(&c, "default", nil)
		assert.NoError(t, err)
	})

	t.Run("secret ref exists, kubernetes secret store, secret extracted", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []componentsapi.MetadataItem{
					{
						Name: "test1",
						SecretKeyRef: componentsapi.SecretKeyRef{
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

		err = processComponentSecrets(&c, "default", client)
		assert.NoError(t, err)

		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, _ := json.Marshal(enc)

		assert.Equal(t, jsonEnc, c.Spec.Metadata[0].Value.Raw)
	})

	t.Run("secret ref exists, default kubernetes secret store, secret extracted", func(t *testing.T) {
		c := componentsapi.Component{
			Spec: componentsapi.ComponentSpec{
				Metadata: []componentsapi.MetadataItem{
					{
						Name: "test1",
						SecretKeyRef: componentsapi.SecretKeyRef{
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

		err = processComponentSecrets(&c, "default", client)
		assert.NoError(t, err)

		enc := base64.StdEncoding.EncodeToString([]byte("value1"))
		jsonEnc, _ := json.Marshal(enc)

		assert.Equal(t, jsonEnc, c.Spec.Metadata[0].Value.Raw)
	})
}

func TestChanGracefullyClose(t *testing.T) {
	t.Run("close updateChan", func(t *testing.T) {
		ch := make(chan *componentsapi.Component)
		instance := initChanGracefully(ch)
		instance.Close()
		assert.Equal(t, true, instance.isClosed)
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
			// Send a component update, give sidecar time to register
			time.Sleep(time.Millisecond * 500)

			api.connLock.Lock()
			defer api.connLock.Unlock()

			for _, connUpdateChan := range api.allConnUpdateChan {
				connUpdateChan <- &c

				// Give sidecar time to register update
				time.Sleep(time.Millisecond * 500)
				close(connUpdateChan)
			}
		}()

		// Start sidecar update loop
		api.ComponentUpdate(&operatorv1pb.ComponentUpdateRequest{
			Namespace: "ns2",
		}, mockSidecar)

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
			// Send a component update, give sidecar time to register
			time.Sleep(time.Millisecond * 500)

			api.connLock.Lock()
			defer api.connLock.Unlock()

			for _, connUpdateChan := range api.allConnUpdateChan {
				connUpdateChan <- &c

				// Give sidecar time to register update
				time.Sleep(time.Millisecond * 500)
				close(connUpdateChan)
			}
		}()

		// Start sidecar update loop
		api.ComponentUpdate(&operatorv1pb.ComponentUpdateRequest{
			Namespace: "ns1",
		}, mockSidecar)

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

		assert.Nil(t, err)
		assert.Equal(t, 1, len(res.GetComponents()))

		var sub resiliencyapi.Resiliency
		err = yaml.Unmarshal(res.GetComponents()[0], &sub)
		assert.Nil(t, err)

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
}
