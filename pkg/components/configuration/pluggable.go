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

package configuration

import (
	c "github.com/dapr/components-contrib/configuration"

	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
)

type configurationStore struct {
	c.Store
}

// NewFromPluggable creates a new ConfigurationStore from a given pluggable component.
func NewFromPluggable(pc pluggable.Component) Configuration {
	return Configuration{
		Names: []string{pc.Name},
		FactoryMethod: func() c.Store {
			return &configurationStore{}
		},
	}
}

func init() {
	pluggable.MustRegister(components.Configuration, NewFromPluggable)
}
