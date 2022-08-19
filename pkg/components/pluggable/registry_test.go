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

	"github.com/stretchr/testify/assert"
)

func TestMain(m *testing.M) {
	registry = make(map[components.Type]func(Component) any)
}

func TestRegister_WithSuccess(t *testing.T) {
	MustRegister[any](components.NameResolution, nil)
	assert.NotNil(t, registry[components.NameResolution])
}

func TestRegister_SameComponentTwice(t *testing.T) {
	MustRegister[any](components.NameResolution, nil)
	assert.NotNil(t, registry[components.NameResolution])
	assert.Panics(t, func() {
		MustRegister[any](components.NameResolution, nil)
	})
}

func TestLoad_UnregisteredComponent(t *testing.T) {
	assert.Panics(t, func() {
		MustLoad[any](Component{})
	})
}

func TestLoad_RegistedComponentUnmatchedTypes(t *testing.T) {
	MustRegister(components.NameResolution, func(c Component) struct{} {
		return struct{}{}
	})

	assert.Panics(t, func() {
		MustLoad[int](Component{
			Type: string(components.NameResolution),
		})
	})
}

func TestLoad_RegistedComponent(t *testing.T) {
	MustRegister(components.NameResolution, func(c Component) struct{} {
		return struct{}{}
	})

	assert.NotPanics(t, func() {
		component := MustLoad[struct{}](Component{
			Type: string(components.NameResolution),
		})
		assert.NotNil(t, component)
	})
}
