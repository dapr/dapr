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

package bindings

import (
	"testing"

	"github.com/dapr/dapr/pkg/components"

	"github.com/stretchr/testify/assert"
)

func TestMustLoadInputBinding(t *testing.T) {
	l := NewInputFromPluggable(components.Pluggable{
		Type: components.InputBinding,
	})
	assert.NotNil(t, l)
}

func TestMustLoadOutputBinding(t *testing.T) {
	l := NewOutputFromPluggable(components.Pluggable{
		Type: components.OutputBinding,
	})
	assert.NotNil(t, l)
}
