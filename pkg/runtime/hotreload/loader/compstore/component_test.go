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

package compstore

import (
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
)

type ComponentStore[T differ.Resource] interface {
	List() []T
}

type component struct {
	compStore *compstore.ComponentStore
}

type endpoint struct {
	compStore *compstore.ComponentStore
}

func NewComponent(compStore *compstore.ComponentStore) ComponentStore[compapi.Component] {
	return &component{
		compStore: compStore,
	}
}

func NewEndpoint(compStore *compstore.ComponentStore) ComponentStore[httpendapi.HTTPEndpoint] {
	return &endpoint{
		compStore: compStore,
	}
}

func (c *component) List() []compapi.Component {
	return c.compStore.ListComponents()
}

func (e *endpoint) List() []httpendapi.HTTPEndpoint {
	return e.compStore.ListHTTPEndpoints()
}
