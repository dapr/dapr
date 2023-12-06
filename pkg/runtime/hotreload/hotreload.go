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
	"errors"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/operator"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload")

type OptionsReloaderDisk struct {
	Config         *config.Configuration
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
}

type OptionsReloaderOperator struct {
	PodName        string
	Namespace      string
	Client         operatorv1.OperatorClient
	Config         *config.Configuration
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
}

type Reloader struct {
	isEnabled            bool
	componentsReconciler *reconciler.Reconciler[componentsapi.Component]
}

func NewDisk(ctx context.Context, opts OptionsReloaderDisk) (*Reloader, error) {
	loader, err := disk.New(ctx, disk.Options{
		Dirs:           opts.Dirs,
		ComponentStore: opts.ComponentStore,
	})
	if err != nil {
		return nil, err
	}

	return &Reloader{
		isEnabled: opts.Config.IsFeatureEnabled(config.HotReload),
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
		isEnabled: opts.Config.IsFeatureEnabled(config.HotReload),
		componentsReconciler: reconciler.NewComponent(reconciler.Options[componentsapi.Component]{
			Loader:     loader,
			CompStore:  opts.ComponentStore,
			Processor:  opts.Processor,
			Authorizer: opts.Authorizer,
		}),
	}
}

func (r *Reloader) Run(ctx context.Context) error {
	if !r.isEnabled {
		log.Debug("Hot reloading disabled")
		<-ctx.Done()
		return nil
	}

	log.Info("Hot reloading enabled. Daprd will reload 'Component' resources on change.")

	return concurrency.NewRunnerManager(
		r.componentsReconciler.Run,
	).Run(ctx)
}

func (r *Reloader) Close() error {
	if r.isEnabled {
		log.Info("Closing hot reloader")
	}
	return errors.Join(r.componentsReconciler.Close())
}
