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

package configuration

import (
	"context"
	"io"
	"sync"

	contribconfig "github.com/dapr/components-contrib/configuration"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compconfig "github.com/dapr/dapr/pkg/components/configuration"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	Registry       *compconfig.Registry
	ComponentStore *compstore.ComponentStore
	Meta           *meta.Meta
}

type configuration struct {
	registry  *compconfig.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta
	lock      sync.Mutex
}

func New(opts Options) *configuration {
	return &configuration{
		registry:  opts.Registry,
		compStore: opts.ComponentStore,
		meta:      opts.Meta,
	}
}

func (c *configuration) Init(ctx context.Context, comp compapi.Component) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	fName := comp.LogName()
	config, err := c.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}
	if config != nil {
		meta, err := c.meta.ToBaseMetadata(comp)
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}
		err = config.Init(ctx, contribconfig.Metadata{Base: meta})
		if err != nil {
			diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
			return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
		}

		c.compStore.AddConfiguration(comp.ObjectMeta.Name, config)
		diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)
	}

	return nil
}

func (c *configuration) Close(comp compapi.Component) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	conf, ok := c.compStore.GetConfiguration(comp.ObjectMeta.Name)
	if !ok {
		return nil
	}

	defer c.compStore.DeleteConfiguration(comp.ObjectMeta.Name)

	closer, ok := conf.(io.Closer)
	if ok && closer != nil {
		if err := closer.Close(); err != nil {
			return err
		}
	}

	return nil
}
