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

package meta

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/modes"
)

func TestMetadataItemsToPropertiesConversion(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		items := []common.NameValuePair{
			{
				Name: "a",
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte("b")},
				},
			},
		}
		m, err := meta.convertItemsToProps(items)
		require.NoError(t, err)
		assert.Len(t, m, 1)
		assert.Equal(t, "b", m["a"])
	})

	t.Run("int", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		items := []common.NameValuePair{
			{
				Name: "a",
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte(strconv.Itoa(6))},
				},
			},
		}
		m, err := meta.convertItemsToProps(items)
		require.NoError(t, err)
		assert.Len(t, m, 1)
		assert.Equal(t, "6", m["a"])
	})

	t.Run("bool", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		items := []common.NameValuePair{
			{
				Name: "a",
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte("true")},
				},
			},
		}
		m, err := meta.convertItemsToProps(items)
		require.NoError(t, err)
		assert.Len(t, m, 1)
		assert.Equal(t, "true", m["a"])
	})

	t.Run("float", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		items := []common.NameValuePair{
			{
				Name: "a",
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte("5.5")},
				},
			},
		}
		m, err := meta.convertItemsToProps(items)
		require.NoError(t, err)
		assert.Len(t, m, 1)
		assert.Equal(t, "5.5", m["a"])
	})

	t.Run("JSON string", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		items := []common.NameValuePair{
			{
				Name: "a",
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte(`"hello there"`)},
				},
			},
		}
		m, err := meta.convertItemsToProps(items)
		require.NoError(t, err)
		assert.Len(t, m, 1)
		assert.Equal(t, "hello there", m["a"])
	})
}

func TestMetadataContainsNamespace(t *testing.T) {
	t.Run("namespace field present", func(t *testing.T) {
		r := ContainsNamespace(
			[]common.NameValuePair{
				{
					Value: common.DynamicValue{
						JSON: v1.JSON{Raw: []byte("{namespace}")},
					},
				},
			},
		)

		assert.True(t, r)
	})

	t.Run("namespace field not present", func(t *testing.T) {
		r := ContainsNamespace(
			[]common.NameValuePair{
				{},
			},
		)

		assert.False(t, r)
	})
}

func TestMetadataOverrideWasmStrictSandbox(t *testing.T) {
	t.Run("original set to false override to true", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode, StrictSandbox: true})
		// component with WasmStrictSandbox set to false
		items := []common.NameValuePair{
			{
				Name: WasmStrictSandboxMetadataKey,
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte(`false`)},
				},
			},
		}
		com := compapi.Component{
			Spec: compapi.ComponentSpec{
				Metadata: items,
			},
		}

		// override WasmStrictSandbox to true
		meta.AddWasmStrictSandbox(&com)

		// check that WasmStrictSandbox is set to true
		base, err := meta.ToBaseMetadata(com)
		require.NoError(t, err)
		assert.Equal(t, "true", base.Properties[WasmStrictSandboxMetadataKey])
	})

	t.Run("global strict sandbox config not set", func(t *testing.T) {
		meta := New(Options{Mode: modes.StandaloneMode})
		// component with WasmStrictSandbox set to false
		items := []common.NameValuePair{
			{
				Name: WasmStrictSandboxMetadataKey,
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte(`true`)},
				},
			},
		}
		com := compapi.Component{
			Spec: compapi.ComponentSpec{
				Metadata: items,
			},
		}

		// set global strictSandbox config
		meta.AddWasmStrictSandbox(&com)

		// check that WasmStrictSandbox is set to true
		base, err := meta.ToBaseMetadata(com)
		require.NoError(t, err)
		assert.Equal(t, "true", base.Properties[WasmStrictSandboxMetadataKey])
	})

	t.Run("auto set strict sandbox to wasm components", func(t *testing.T) {
		// register test wasm component
		components.RegisterWasmComponentType(components.CategoryMiddleware, "test")
		meta := New(Options{Mode: modes.StandaloneMode, StrictSandbox: true})
		// component with WasmStrictSandbox set to false
		items := []common.NameValuePair{
			{
				Name: WasmStrictSandboxMetadataKey,
				Value: common.DynamicValue{
					JSON: v1.JSON{Raw: []byte(`false`)},
				},
			},
		}
		com := compapi.Component{
			Spec: compapi.ComponentSpec{
				Type:     "middleware.test",
				Metadata: items,
			},
		}

		// component that is not registered as a wasm component
		noneWasmComp := compapi.Component{
			Spec: compapi.ComponentSpec{
				Type:     "middleware.none",
				Metadata: []common.NameValuePair{},
			},
		}

		wasm, err := meta.ToBaseMetadata(com)
		noneWasm, err2 := meta.ToBaseMetadata(noneWasmComp)
		require.NoError(t, err)
		require.NoError(t, err2)
		assert.Equal(t, "true", wasm.Properties[WasmStrictSandboxMetadataKey])
		assert.Equal(t, "", noneWasm.Properties[WasmStrictSandboxMetadataKey])
	})
}
