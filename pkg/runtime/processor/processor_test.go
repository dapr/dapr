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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/registry"
)

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
			fakeComp := compapi.Component{
				Spec: compapi.ComponentSpec{
					Type:    tt.specType,
					Version: "v1",
				},
			}
			assert.Equal(t, string(p.category(fakeComp)), tt.category)
		})
	}
}
