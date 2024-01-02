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

package operator

import (
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.loader.operator")

type Options struct {
	PodName        string
	Namespace      string
	ComponentStore *compstore.ComponentStore
	OperatorClient operatorpb.OperatorClient
}

type operator struct {
	component *resource[componentsapi.Component]
}

func New(opts Options) loader.Interface {
	return &operator{
		component: newResource[componentsapi.Component](opts, loadercompstore.NewComponent(opts.ComponentStore), new(component)),
	}
}

func (o *operator) Close() error {
	return o.component.close()
}

func (o *operator) Components() loader.Loader[componentsapi.Component] {
	return o.component
}
