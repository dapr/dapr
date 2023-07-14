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
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dapr/dapr/pkg/apis/common"
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
		m := meta.convertItemsToProps(items)
		assert.Equal(t, 1, len(m))
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
		m := meta.convertItemsToProps(items)
		assert.Equal(t, 1, len(m))
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
		m := meta.convertItemsToProps(items)
		assert.Equal(t, 1, len(m))
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
		m := meta.convertItemsToProps(items)
		assert.Equal(t, 1, len(m))
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
		m := meta.convertItemsToProps(items)
		assert.Equal(t, 1, len(m))
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
