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

	contribconfig "github.com/dapr/components-contrib/configuration"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compconfig "github.com/dapr/dapr/pkg/components/configuration"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type configuration struct {
	registry  *compconfig.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
}

func (c *configuration) init(ctx context.Context, comp compapi.Component) error {
	fName := comp.LogName()
	config, err := c.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}
	if config != nil {
		err := config.Init(ctx, contribconfig.Metadata{Base: c.meta.ToBaseMetadata(comp)})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}

		c.compStore.AddConfiguration(comp.ObjectMeta.Name, config)
		diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)
	}

	return nil
}
