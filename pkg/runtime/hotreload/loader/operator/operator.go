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
	"errors"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/compstore"
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
	component *generic[compapi.Component]
	endpoint  *generic[httpendapi.HTTPEndpoint]
}

func New(opts Options) loader.Interface {
	return &operator{
		component: newGeneric[compapi.Component](opts, loadercompstore.NewComponent(opts.ComponentStore), new(component)),
		endpoint:  newGeneric[httpendapi.HTTPEndpoint](opts, loadercompstore.NewHTTPEndpoint(opts.ComponentStore), new(endpoint)),
	}
}

func (o *operator) Close() error {
	var errs []error
	if err := o.component.close(); err != nil {
		errs = append(errs, err)
	}

	if err := o.endpoint.close(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (o *operator) Components() loader.Loader[compapi.Component] {
	return o.component
}

func (o *operator) HTTPEndpoints() loader.Loader[httpendapi.HTTPEndpoint] {
	return o.endpoint
}
