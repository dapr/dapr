// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package api

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/client/clientset/versioned/scheme"
)

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
