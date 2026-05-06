/*
Copyright 2024 The Dapr Authors
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

package store

import (
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type resiliencies struct {
	compStore *compstore.ComponentStore
}

func NewResiliencies(compStore *compstore.ComponentStore) Store[resiliencyapi.Resiliency] {
	return &resiliencies{
		compStore: compStore,
	}
}

func (r *resiliencies) List() []resiliencyapi.Resiliency {
	return r.compStore.ListResiliencyResources()
}
