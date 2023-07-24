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

package hotreload

import (
	"context"
	"errors"
	"os"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/concurrency"
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

const (
	// hot reloading is currently unsupported, but
	// setting this environment variable restores the
	// partial hot reloading support for k8s.
	hotReloadingEnvVar = "DAPR_ENABLE_HOT_RELOADING"
)

var log = logger.NewLogger("dapr.runtime.hotreload")

type OptionsDisk struct {
	Dirs           []string
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Channels       *channels.Channels
}

type OptionsOperator struct {
	PodName        string
	Namespace      string
	OperatorClient operatorpb.OperatorClient
	ComponentStore *compstore.ComponentStore
	Authorizer     *authorizer.Authorizer
	Processor      *processor.Processor
	Channels       *channels.Channels
}

type Reloader struct {
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
	if env, ok := os.LookupEnv(hotReloadingEnvVar); !ok || (env != "true" && env != `"true"`) {
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
