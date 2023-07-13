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

package processor

import (
	"context"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compbindings "github.com/dapr/dapr/pkg/components/bindings"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

const (
	BindingDirection  = "direction"
	BindingTypeInput  = "input"
	BindingTypeOutput = "output"
)

type binding struct {
	registry  *compbindings.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
}

func (b *binding) init(ctx context.Context, comp compapi.Component) error {
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

func (b *binding) initInputBinding(ctx context.Context, comp compapi.Component) error {
	if !b.isBindingOfDirection(BindingTypeInput, comp.Spec.Metadata) {
		return nil
	}

	fName := comp.LogName()
	binding, err := b.registry.CreateInputBinding(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}
	err = binding.Init(ctx, bindings.Metadata{Base: b.meta.ToBaseMetadata(comp)})
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
	return nil
}

func (b *binding) initOutputBinding(ctx context.Context, comp compapi.Component) error {
	if !b.isBindingOfDirection(BindingTypeOutput, comp.Spec.Metadata) {
		return nil
	}

	fName := comp.LogName()
	binding, err := b.registry.CreateOutputBinding(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if binding != nil {
		err := binding.Init(context.TODO(), bindings.Metadata{Base: b.meta.ToBaseMetadata(comp)})
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
		if strings.EqualFold(m.Name, BindingDirection) {
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
