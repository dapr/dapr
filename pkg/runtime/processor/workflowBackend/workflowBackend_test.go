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

package workflowBackend_test

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
	wfs "github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	backendLoader "github.com/dapr/dapr/pkg/components/workflowBackend"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestInitWorkflowBackend(t *testing.T) {
	reg := registry.New(registry.NewOptions().WithWorkflowBackends(backendLoader.NewRegistry()))
	compStore := compstore.New()
	proc := processor.New(processor.Options{
		Registry:       reg,
		ComponentStore: compStore,
		GlobalConfig:   new(config.Configuration),
		Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
	})

	bytes := make([]byte, 32)
	rand.Read(bytes)

	connectionString := hex.EncodeToString(bytes)

	mockWorkflowBackendComponent := func(name string) compapi.Component {
		return compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: compapi.ComponentSpec{
				Type:    "workflowbackend.mockWorkflowBackend",
				Version: "v1",
				Metadata: []common.NameValuePair{
					{
						Name: "orchestrationLockTimeout",
						Value: common.DynamicValue{
							JSON: apiextv1.JSON{Raw: []byte("1000ms")},
						},
					},
					{
						Name: "activityLockTimeout",
						Value: common.DynamicValue{
							JSON: apiextv1.JSON{Raw: []byte("1000ms")},
						},
					},
					{
						Name: "connectionString",
						Value: common.DynamicValue{
							JSON: apiextv1.JSON{Raw: []byte(connectionString)},
						},
					},
				},
			},
		}
	}

	t.Run("test init workflow backend", func(t *testing.T) {
		// setup
		initMockWorkflowBackendForRegistry(reg, "noerror", connectionString, nil)

		// act
		err := proc.Init(context.TODO(), mockWorkflowBackendComponent("noerror"))

		// assert
		require.NoError(t, err, "expected no error")
	})

	t.Run("test init workflow backend error", func(t *testing.T) {
		// setup
		initMockWorkflowBackendForRegistry(reg, "error", connectionString, assert.AnError)

		// act
		err := proc.Init(context.TODO(), mockWorkflowBackendComponent("error"))

		// assert
		require.Error(t, err, "expected error")
		assert.Equal(t, err.Error(), rterrors.NewInit(rterrors.InitComponentFailure, "error (workflowbackend.mockWorkflowBackend/v1)", assert.AnError).Error(), "expected error strings to match")
	})
}

func initMockWorkflowBackendForRegistry(reg *registry.Registry, name, connectionString string, e error) *daprt.MockWorkflowBackend {
	mockWorkflowBackend := new(daprt.MockWorkflowBackend)

	reg.WorkflowBackends().RegisterComponent(
		func(_ logger.Logger) wfs.WorkflowBackend {
			return mockWorkflowBackend
		},
		"mockWorkflowBackend",
	)

	expectedMetadata := wfs.Metadata{Base: metadata.Base{
		Name: name,
		Properties: map[string]string{
			"orchestrationLockTimeout": "1000ms",
			"activityLockTimeout":      "1000ms",
			"connectionString":         connectionString,
		},
	}}
	expectedMetadataUppercase := wfs.Metadata{Base: metadata.Base{
		Name: name,
		Properties: map[string]string{
			"orchestrationLockTimeout": "1000ms",
			"activityLockTimeout":      "1000ms",
			"CONNECTIONSTRING":         connectionString,
		},
	}}

	mockWorkflowBackend.On("Init", expectedMetadata).Return(e)
	mockWorkflowBackend.On("Init", expectedMetadataUppercase).Return(e)

	return mockWorkflowBackend
}
