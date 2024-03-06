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

package hotreload

import (
	"context"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/operator"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/concurrency"
)

type OptionsReloaderDisk struct {
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
}

type OptionsReloaderOperator struct {
	PodName        string
	Namespace      string
	Client         operatorv1.OperatorClient
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
}

type Reloader struct {
	componentsLoader     loader.Interface
	componentsReconciler *reconciler.Reconciler[componentsapi.Component]
}

func NewDisk(opts OptionsReloaderDisk) (*Reloader, error) {
	loader, err := disk.New(disk.Options{
		Dirs:           opts.Dirs,
		ComponentStore: opts.ComponentStore,
	})
	if err != nil {
		return nil, err
	}
	return &Reloader{
		componentsLoader: loader,
		componentsReconciler: reconciler.NewComponent(reconciler.Options[componentsapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
		}),
	}, nil
}

func NewOperator(opts OptionsReloaderOperator) *Reloader {
	loader := operator.New(operator.Options{
		PodName:        opts.PodName,
		Namespace:      opts.Namespace,
		ComponentStore: opts.ComponentStore,
		OperatorClient: opts.Client,
	})

	return &Reloader{
		componentsLoader: loader,
		componentsReconciler: reconciler.NewComponent(reconciler.Options[componentsapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
		}),
	}
}

func (r *Reloader) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		r.componentsLoader.Run,
		r.componentsReconciler.Run,
	).Run(ctx)
}
