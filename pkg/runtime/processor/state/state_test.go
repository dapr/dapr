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

package state_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/metadata"
	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/encryption"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestInitState(t *testing.T) {
	reg := registry.New(registry.NewOptions().WithStateStores(stateLoader.NewRegistry()))
	compStore := compstore.New()
	proc := processor.New(processor.Options{
		Registry:       reg,
		ComponentStore: compStore,
		GlobalConfig:   new(config.Configuration),
		Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
	})

	bytes := make([]byte, 32)
	rand.Read(bytes)

	primaryKey := hex.EncodeToString(bytes)

	mockStateComponent := func(name string) compapi.Component {
		return compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: compapi.ComponentSpec{
				Type:    "state.mockState",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{
						Name: "actorstatestore",
						Value: common.DynamicValue{
							JSON: apiextv1.JSON{Raw: []byte("true")},
						},
					},
					{
						Name: "primaryEncryptionKey",
						Value: common.DynamicValue{
							JSON: apiextv1.JSON{Raw: []byte(primaryKey)},
						},
					},
				},
			},
			Auth: compapi.Auth{
				SecretStore: "mockSecretStore",
			},
		}
	}

	t.Run("test init state store", func(t *testing.T) {
		// setup
		initMockStateStoreForRegistry(reg, "noerror", primaryKey, nil)

		// act
		err := proc.Init(context.TODO(), mockStateComponent("noerror"))

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init state store error", func(t *testing.T) {
		// setup
		initMockStateStoreForRegistry(reg, "error", primaryKey, assert.AnError)

		// act
		err := proc.Init(context.TODO(), mockStateComponent("error"))

		// assert
		require.Error(t, err, "expected error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "error (state.mockState/v1)", assert.AnError).Error(), "expected error strings to match")
	})

	t.Run("test init state store, encryption not enabled", func(t *testing.T) {
		// setup
		initMockStateStoreForRegistry(reg, "noencryption", primaryKey, nil)

		// act
		err := proc.Init(context.TODO(), mockStateComponent("noencryption"))
		ok := encryption.EncryptedStateStore("noencryption")

		// assert
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("test init state store, encryption enabled", func(t *testing.T) {
		// setup
		initMockStateStoreForRegistry(reg, "encryption", primaryKey, nil)

		compStore.AddSecretStore("mockSecretStore", &mock.SecretStore{})

		err := proc.Init(context.TODO(), mockStateComponent("encryption"))
		ok := encryption.EncryptedStateStore("encryption")

		// assert
		require.NoError(t, err)
		assert.True(t, ok)
	})
}

func initMockStateStoreForRegistry(reg *registry.Registry, name, encryptKey string, e error) *daprt.MockStateStore {
	mockStateStore := new(daprt.MockStateStore)

	reg.StateStores().RegisterComponent(
		func(_ logger.Logger) contribstate.Store {
			return mockStateStore
		},
		"mockState",
	)

	expectedMetadata := contribstate.Metadata{Base: metadata.Base{
		Name: name,
		Properties: map[string]string{
			"actorstatestore":      "true",
			"primaryEncryptionKey": encryptKey,
		},
	}}
	expectedMetadataUppercase := contribstate.Metadata{Base: metadata.Base{
		Name: name,
		Properties: map[string]string{
			"ACTORSTATESTORE":      "true",
			"primaryEncryptionKey": encryptKey,
		},
	}}

	mockStateStore.On("Init", expectedMetadata).Return(e)
	mockStateStore.On("Init", expectedMetadataUppercase).Return(e)

	return mockStateStore
}
