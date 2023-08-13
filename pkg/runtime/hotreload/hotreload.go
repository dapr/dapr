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

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/config"
	operatorpb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/operator"
	"github.com/dapr/dapr/pkg/runtime/hotreload/reconciler"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload")

type OptionsDisk struct {
	Config         *config.Configuration
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Channels       *channels.Channels
}

type OptionsOperator struct {
	Config         *config.Configuration
	PodName        string
	Namespace      string
	OperatorClient operatorpb.OperatorClient
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Channels       *channels.Channels
}

type Reloader struct {
	isEnabled  bool
	components *reconciler.Reconciler[compapi.Component]
	endpoints  *reconciler.Reconciler[httpendapi.HTTPEndpoint]
}

func NewDisk(opts OptionsDisk) (*Reloader, error) {
	loader, err := disk.New(disk.Options{
		Dirs:           opts.Dirs,
		ComponentStore: opts.ComponentStore,
	})
	if err != nil {
		return nil, err
	}

	return &Reloader{
		isEnabled: opts.Config.IsFeatureEnabled(config.HotReload),
		components: reconciler.NewComponent(reconciler.Options[compapi.Component]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Processor: opts.Processor,
		}, opts.Authorizer),
		endpoints: reconciler.NewHTTPEndpoint(reconciler.Options[httpendapi.HTTPEndpoint]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Processor: opts.Processor,
		}, opts.Channels),
	}, nil
}

func NewOperator(opts OptionsOperator) *Reloader {
	loader := operator.New(operator.Options{
		PodName:        opts.PodName,
		Namespace:      opts.Namespace,
		OperatorClient: opts.OperatorClient,
		ComponentStore: opts.ComponentStore,
	})

	return &Reloader{
		isEnabled: opts.Config.IsFeatureEnabled(config.HotReload),
		components: reconciler.NewComponent(reconciler.Options[compapi.Component]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Processor: opts.Processor,
		}, opts.Authorizer),
		endpoints: reconciler.NewHTTPEndpoint(reconciler.Options[httpendapi.HTTPEndpoint]{
			Loader:    loader,
			CompStore: opts.ComponentStore,
			Processor: opts.Processor,
		}, opts.Channels),
	}
}

func (r *Reloader) Run(ctx context.Context) error {
	if !r.isEnabled {
		log.Debug("Hot reloading disabled")
		<-ctx.Done()
		return nil
	}

	log.Info("Hot reloading enabled")

	return concurrency.NewRunnerManager(
		r.components.Run,
		r.endpoints.Run,
	).Run(ctx)
}

func (r *Reloader) Close() error {
	return errors.Join(r.components.Close(), r.endpoints.Close())
}
