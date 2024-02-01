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

package binding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compbindings "github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/kit/logger"
)

const (
	ComponentDirection  = "direction"
	ComponentTypeInput  = "input"
	ComponentTypeOutput = "output"

	// output bindings concurrency.
	ConcurrencyParallel   = "parallel"
	ConcurrencySequential = "sequential"
)

var log = logger.NewLogger("dapr.runtime.processor.binding")

type Options struct {
	IsHTTP bool

	Registry       *compbindings.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
	Resiliency     resiliency.Provider
	GRPC           *manager.Manager
	TracingSpec    *config.TracingSpec
	Channels       *channels.Channels
}

type binding struct {
	isHTTP bool

	registry    *compbindings.Registry
	resiliency  resiliency.Provider
	compStore   *compstore.ComponentStore
	meta        *meta.Meta
	channels    *channels.Channels
	tracingSpec *config.TracingSpec
	grpc        *manager.Manager

	lock            sync.Mutex
	readingBindings bool
	stopForever     bool

	subscribeBindingList []string
	inputCancels         map[string]context.CancelFunc
	wg                   sync.WaitGroup
}

func New(opts Options) *binding {
	return &binding{
		registry:     opts.Registry,
		compStore:    opts.ComponentStore,
		meta:         opts.Meta,
		isHTTP:       opts.IsHTTP,
		resiliency:   opts.Resiliency,
		tracingSpec:  opts.TracingSpec,
		grpc:         opts.GRPC,
		channels:     opts.Channels,
		inputCancels: make(map[string]context.CancelFunc),
	}
}

func (b *binding) Init(ctx context.Context, comp compapi.Component) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	var found bool

	if b.registry.HasInputBinding(comp.Spec.Type, comp.Spec.Version) {
		if err := b.initInputBinding(ctx, comp); err != nil {
			return err
		}
		found = true
	}

	if b.registry.HasOutputBinding(comp.Spec.Type, comp.Spec.Version) {
		if err := b.initOutputBinding(ctx, comp); err != nil {
			return err
		}
		found = true
	}

	if !found {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return fmt.Errorf("couldn't find binding %s", comp.LogName())
	}

	return nil
}

func (b *binding) Close(comp compapi.Component) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	var errs []error

	inbinding, ok := b.compStore.GetInputBinding(comp.Name)
	if ok {
		defer b.compStore.DeleteInputBinding(comp.Name)
		if cancel := b.inputCancels[comp.Name]; cancel != nil {
			cancel()
		}
		delete(b.inputCancels, comp.Name)
		if err := inbinding.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	outbinding, ok := b.compStore.GetOutputBinding(comp.Name)
	if ok {
		defer b.compStore.DeleteOutputBinding(comp.Name)
		if err := b.closeOutputBinding(outbinding); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (b *binding) closeOutputBinding(binding bindings.OutputBinding) error {
	closer, ok := binding.(io.Closer)
	if ok && closer != nil {
		return closer.Close()
	}
	return nil
}

func (b *binding) initInputBinding(ctx context.Context, comp compapi.Component) error {
	if !b.isBindingOfDirection(ComponentTypeInput, comp.Spec.Metadata) {
		return nil
	}

	fName := comp.LogName()
	binding, err := b.registry.CreateInputBinding(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	meta, err := b.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	err = binding.Init(ctx, bindings.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	log.Infof("successful init for input binding (%s)", comp.LogName())
	b.compStore.AddInputBindingRoute(comp.Name, comp.Name)
	for _, item := range comp.Spec.Metadata {
		if item.Name == "route" {
			b.compStore.AddInputBindingRoute(comp.ObjectMeta.Name, item.Value.String())
			break
		}
	}
	b.compStore.AddInputBinding(comp.Name, binding)
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	if b.readingBindings {
		return b.startInputBinding(comp, binding)
	}

	return nil
}

func (b *binding) initOutputBinding(ctx context.Context, comp compapi.Component) error {
	if !b.isBindingOfDirection(ComponentTypeOutput, comp.Spec.Metadata) {
		return nil
	}

	fName := comp.LogName()
	binding, err := b.registry.CreateOutputBinding(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if binding != nil {
		meta, err := b.meta.ToBaseMetadata(comp)
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}

		err = binding.Init(ctx, bindings.Metadata{Base: meta})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}
		log.Infof("successful init for output binding (%s)", comp.LogName())
		b.compStore.AddOutputBinding(comp.ObjectMeta.Name, binding)
		diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)
	}
	return nil
}

func (b *binding) isBindingOfDirection(direction string, metadata []common.NameValuePair) bool {
	directionFound := false

	for _, m := range metadata {
		if strings.EqualFold(m.Name, ComponentDirection) {
			directionFound = true

			directions := strings.Split(m.Value.String(), ",")
			for _, d := range directions {
				if strings.TrimSpace(strings.ToLower(d)) == direction {
					return true
				}
			}
		}
	}

	return !directionFound
}
