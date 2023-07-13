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
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
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

func TestInitBindings(t *testing.T) {
	t.Run("single input binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		reg.Bindings().RegisterInputBinding(
			func(_ logger.Logger) bindings.InputBinding {
				return &daprt.MockBinding{}
			},
			"testInputBinding",
		)
		proc := New(Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testInputBinding"
		c.Spec.Type = "bindings.testInputBinding"
		err := proc.One(context.TODO(), c)
		assert.NoError(t, err)
	})

	t.Run("single output binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		reg.Bindings().RegisterOutputBinding(func(_ logger.Logger) bindings.OutputBinding {
			return &daprt.MockBinding{}
		},
			"testOutputBinding",
		)

		proc := New(Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testOutputBinding"
		c.Spec.Type = "bindings.testOutputBinding"
		err := proc.One(context.TODO(), c)
		assert.NoError(t, err)
	})

	t.Run("one input binding, one output binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		reg.Bindings().RegisterInputBinding(func(_ logger.Logger) bindings.InputBinding {
			return &daprt.MockBinding{}
		},
			"testinput",
		)

		reg.Bindings().RegisterOutputBinding(
			func(_ logger.Logger) bindings.OutputBinding {
				return &daprt.MockBinding{}
			},
			"testoutput",
		)

		proc := New(Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})

		input := compapi.Component{}
		input.ObjectMeta.Name = "testinput"
		input.Spec.Type = "bindings.testinput"
		err := proc.One(context.TODO(), input)
		assert.NoError(t, err)

		output := compapi.Component{}
		output.ObjectMeta.Name = "testinput"
		output.Spec.Type = "bindings.testoutput"
		err = proc.One(context.TODO(), output)
		assert.NoError(t, err)
	})

	t.Run("one not exist binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		// no binding registered, just try to init a not exist binding

		proc := New(Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			Meta:           meta.New(meta.Options{}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testNotExistBinding"
		c.Spec.Type = "bindings.testNotExistBinding"
		err := proc.One(context.TODO(), c)
		assert.Error(t, err)
	})
}
