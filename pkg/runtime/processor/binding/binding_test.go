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

package binding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dapr/dapr/pkg/apis/common"
)

func TestIsBindingOfDirection(t *testing.T) {
	t.Run("no direction in metadata for input binding", func(t *testing.T) {
		m := []common.NameValuePair{}
		r := (new(binding)).isBindingOfDirection("input", m)

		assert.True(t, r)
	})

	t.Run("no direction in metadata for output binding", func(t *testing.T) {
		m := []common.NameValuePair{}
		r := (new(binding)).isBindingOfDirection("output", m)

		assert.True(t, r)
	})

	t.Run("input direction in metadata", func(t *testing.T) {
		m := []common.NameValuePair{
			{
				Name: "direction",
				Value: common.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("input"),
					},
				},
			},
		}
		r := (new(binding)).isBindingOfDirection("input", m)
		f := (new(binding)).isBindingOfDirection("output", m)

		assert.True(t, r)
		assert.False(t, f)
	})

	t.Run("output direction in metadata", func(t *testing.T) {
		m := []common.NameValuePair{
			{
				Name: "direction",
				Value: common.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("output"),
					},
				},
			},
		}
		r := (new(binding)).isBindingOfDirection("output", m)
		f := (new(binding)).isBindingOfDirection("input", m)

		assert.True(t, r)
		assert.False(t, f)
	})

	t.Run("input and output direction in metadata", func(t *testing.T) {
		m := []common.NameValuePair{
			{
				Name: "direction",
				Value: common.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("input, output"),
					},
				},
			},
		}
		r := (new(binding)).isBindingOfDirection("output", m)
		f := (new(binding)).isBindingOfDirection("input", m)

		assert.True(t, r)
		assert.True(t, f)
	})

	t.Run("invalid direction for input binding", func(t *testing.T) {
		m := []common.NameValuePair{
			{
				Name: "direction",
				Value: common.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("aaa"),
					},
				},
			},
		}
		f := (new(binding)).isBindingOfDirection("input", m)
		assert.False(t, f)
	})

	t.Run("invalid direction for output binding", func(t *testing.T) {
		m := []common.NameValuePair{
			{
				Name: "direction",
				Value: common.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte("aaa"),
					},
				},
			},
		}
		f := (new(binding)).isBindingOfDirection("output", m)
		assert.False(t, f)
	})
}
