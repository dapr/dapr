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

package wfbackend_test

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
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
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
	reg := registry.New(registry.NewOptions().WithWorkflowBackends(wfbe.NewRegistry()))
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
		comp := mockWorkflowBackendComponent("noerror")

		// act
		initErr := proc.Init(context.TODO(), comp)

		// assert
		require.NoError(t, initErr, "expected no error")
	})

	t.Run("test close workflow backend", func(t *testing.T) {
		// setup
		newReg := registry.New(registry.NewOptions().WithWorkflowBackends(wfbe.NewRegistry()))
		newCompStore := compstore.New()
		newProc := processor.New(processor.Options{
			Registry:       newReg,
			ComponentStore: newCompStore,
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
		})
		initMockWorkflowBackendForRegistry(newReg, "noerror", connectionString, nil)
		comp := mockWorkflowBackendComponent("noerror")

		// act
		initErr := newProc.Init(context.TODO(), comp)
		closeErr := newProc.Close(comp)

		// assert
		require.NoError(t, initErr, "expected no error")
		require.NoError(t, closeErr, "expected no error")
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

	t.Run("test init workflow backend registry error", func(t *testing.T) {
		// setup
		newReg := registry.New(registry.NewOptions().WithWorkflowBackends(wfbe.NewRegistry()))
		newCompStore := compstore.New()
		newProc := processor.New(processor.Options{
			Registry:       newReg,
			ComponentStore: newCompStore,
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
		})

		// act
		err := newProc.Init(context.TODO(), mockWorkflowBackendComponent("error1"))

		// assert
		require.Error(t, err, "expected error")
		assert.Equal(t, "couldn't find wokflow backend workflowbackend.mockWorkflowBackend/v1", err.Error(), "expected error strings to match")
	})

	t.Run("test workflow backend component info not nil", func(t *testing.T) {
		// setup
		newReg := registry.New(registry.NewOptions().WithWorkflowBackends(wfbe.NewRegistry()))
		newCompStore := compstore.New()
		newProc := processor.New(processor.Options{
			Registry:       newReg,
			ComponentStore: newCompStore,
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
		})
		initMockWorkflowBackendForRegistry(newReg, "noerror", connectionString, nil)
		comp := mockWorkflowBackendComponent("noerror")
		be := newProc.WorkflowBackend()

		// act
		initErr := newProc.Init(context.TODO(), comp)
		componentInfo, ok := be.WorkflowBackendComponentInfo()

		// assert
		require.NoError(t, initErr, "expected no error")
		require.True(t, ok, "expected component info ok")
		assert.NotNil(t, componentInfo, "expected component info not nil")
		assert.Equal(t, "workflowbackend.mockWorkflowBackend", componentInfo.WorkflowBackendType, "expected workflow backend type to match")
	})

	t.Run("test workflow backend component info nil", func(t *testing.T) {
		// setup
		newReg := registry.New(registry.NewOptions().WithWorkflowBackends(wfbe.NewRegistry()))
		newCompStore := compstore.New()
		newProc := processor.New(processor.Options{
			Registry:       newReg,
			ComponentStore: newCompStore,
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{Mode: modes.StandaloneMode}),
		})
		be := newProc.WorkflowBackend()

		// act
		componentInfo, ok := be.WorkflowBackendComponentInfo()

		// assert
		assert.Nil(t, componentInfo, "expected component info not nil")
		require.False(t, ok, "expected component info not ok")
	})
}

func initMockWorkflowBackendForRegistry(reg *registry.Registry, name, connectionString string, e error) *daprt.MockWorkflowBackend {
	mockWorkflowBackend := new(daprt.MockWorkflowBackend)

	reg.WorkflowBackends().RegisterComponent(
		func(_ logger.Logger) wfbe.WorkflowBackend {
			return mockWorkflowBackend
		},
		"mockWorkflowBackend",
	)

	expectedMetadata := wfbe.Metadata{Base: metadata.Base{
		Name: name,
		Properties: map[string]string{
			"orchestrationLockTimeout": "1000ms",
			"activityLockTimeout":      "1000ms",
			"connectionString":         connectionString,
		},
	}}
	expectedMetadataUppercase := wfbe.Metadata{Base: metadata.Base{
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
