/*
Copyright 2024 The Dapr Authors
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

package conversation

import (
	"context"
	"io"

	contribconversation "github.com/dapr/components-contrib/conversation"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compconversation "github.com/dapr/dapr/pkg/components/conversation"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
)

type Options struct {
	Registry *compconversation.Registry
	Store    *compstore.ComponentStore
	Meta     *meta.Meta
}

type conversation struct {
	registry *compconversation.Registry
	store    *compstore.ComponentStore
	meta     *meta.Meta
}

func New(opts Options) *conversation {
	return &conversation{
		registry: opts.Registry,
		store:    opts.Store,
		meta:     opts.Meta,
	}
}

func (c *conversation) Init(ctx context.Context, comp compapi.Component) error {
	// create the component
	fName := comp.LogName()
	conversate, err := c.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	if conversate == nil {
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	// initialization
	meta, err := c.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	err = conversate.Init(ctx, contribconversation.Metadata{Base: meta})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	c.store.AddConversation(comp.ObjectMeta.Name, conversate)

	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (c *conversation) Close(comp compapi.Component) error {
	conversate, ok := c.store.GetConversation(comp.ObjectMeta.Name)
	if !ok {
		return nil
	}

	defer c.store.DeleteConversation(comp.ObjectMeta.Name)

	closer, ok := conversate.(io.Closer)
	if ok && closer != nil {
		if err := closer.Close(); err != nil {
			return err
		}
	}

	return nil
}
