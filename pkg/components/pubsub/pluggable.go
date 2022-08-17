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

package pubsub

import (
	p "github.com/dapr/components-contrib/pubsub"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	"github.com/dapr/dapr/pkg/components/pluggable"
)

type mpubsub struct {
	p.PubSub
}

// NewFromPluggable creates a new PubSub from a given pluggable component.
func NewFromPluggable(pc components_v1alpha1.PluggableComponent) PubSub {
	return PubSub{
		Names: []string{pc.Name},
		FactoryMethod: func() p.PubSub {
			return &mpubsub{}
		},
	}
}

func init() {
	pluggable.Register(components.PubSub, NewFromPluggable)
}
