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
	"context"
	"errors"
	"sync/atomic"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
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
	components    *resource[componentsapi.Component]
	subscriptions *resource[subapi.Subscription]

	running atomic.Bool
}

func New(opts Options) loader.Interface {
	return &operator{
		components:    newResource[componentsapi.Component](opts, loadercompstore.NewComponents(opts.ComponentStore), new(components)),
		subscriptions: newResource[subapi.Subscription](opts, loadercompstore.NewSubscriptions(opts.ComponentStore), new(subscriptions)),
	}
}

func (o *operator) Run(ctx context.Context) error {
	if !o.running.CompareAndSwap(false, true) {
		return errors.New("already running")
	}

	<-ctx.Done()
	return errors.Join(o.components.close(), o.subscriptions.close())
}

func (o *operator) Components() loader.Loader[componentsapi.Component] {
	return o.components
}

func (o *operator) Subscriptions() loader.Loader[subapi.Subscription] {
	return o.subscriptions
}
