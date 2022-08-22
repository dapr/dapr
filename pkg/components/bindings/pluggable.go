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
	b "github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/components"
)

type inputBinding struct {
	b.InputBinding
}

// NewInputFromPluggable creates a new InputBinding from a given pluggable component.
func NewInputFromPluggable(pc components.Pluggable) InputBinding {
	return InputBinding{
		Names: []string{pc.Name},
		FactoryMethod: func() b.InputBinding {
			return &inputBinding{}
		},
	}
}

type outputBinding struct {
	b.OutputBinding
}

// NewOutputFromPluggable creates a new OutputBinding from a given pluggable component.
func NewOutputFromPluggable(pc components.Pluggable) OutputBinding {
	return OutputBinding{
		Names: []string{pc.Name},
		FactoryMethod: func() b.OutputBinding {
			return &outputBinding{}
		},
	}
}
