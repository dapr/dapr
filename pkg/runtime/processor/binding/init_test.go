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

package binding_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func TestInitBindings(t *testing.T) {
	t.Run("single input binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		reg.Bindings().RegisterInputBinding(
			func(_ logger.Logger) bindings.InputBinding {
				return &daprt.MockBinding{}
			},
			"testInputBinding",
		)
		proc := processor.New(processor.Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{}),
			GRPC:           manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: 0}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testInputBinding"
		c.Spec.Type = "bindings.testInputBinding"
		err := proc.Init(context.TODO(), c)
		require.NoError(t, err)
	})

	t.Run("single output binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		reg.Bindings().RegisterOutputBinding(func(_ logger.Logger) bindings.OutputBinding {
			return &daprt.MockBinding{}
		},
			"testOutputBinding",
		)

		proc := processor.New(processor.Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testOutputBinding"
		c.Spec.Type = "bindings.testOutputBinding"
		err := proc.Init(context.TODO(), c)
		require.NoError(t, err)
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

		proc := processor.New(processor.Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{}),
			GRPC:           manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: 0}),
		})

		input := compapi.Component{}
		input.ObjectMeta.Name = "testinput"
		input.Spec.Type = "bindings.testinput"
		err := proc.Init(context.TODO(), input)
		require.NoError(t, err)

		output := compapi.Component{}
		output.ObjectMeta.Name = "testoutput"
		output.Spec.Type = "bindings.testoutput"
		err = proc.Init(context.TODO(), output)
		require.NoError(t, err)
	})

	t.Run("one not exist binding", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		// no binding registered, just try to init a not exist binding

		proc := processor.New(processor.Options{
			Registry:       reg,
			ComponentStore: compstore.New(),
			GlobalConfig:   new(config.Configuration),
			Meta:           meta.New(meta.Options{}),
			GRPC:           manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: 0}),
		})

		c := compapi.Component{}
		c.ObjectMeta.Name = "testNotExistBinding"
		c.Spec.Type = "bindings.testNotExistBinding"
		err := proc.Init(context.TODO(), c)
		require.Error(t, err)
	})
}
