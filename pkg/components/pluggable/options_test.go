/*
Copyright 2021 The Dapr Authors
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

package pluggable

import (
	"testing"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/state"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	t.Run("all components should have its own registry when set", func(t *testing.T) {
		opts := registryOpts{
			registries: make(map[components.PluggableType]func(components.Pluggable)),
		}
		allOpts := []Option{
			WithStateStoreRegistry(state.NewRegistry()),
		}
		for _, opt := range allOpts {
			opt(&opts)
		}
		assert.Len(t, opts.registries, len(components.WellKnownTypes))

		for _, tp := range components.WellKnownTypes {
			assert.NotNil(t, opts.registries[tp])
		}
	})
}
