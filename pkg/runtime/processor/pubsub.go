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
	"strings"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	comppubsub "github.com/dapr/dapr/pkg/components/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/scopes"
)

type pubsub struct {
	registry  *comppubsub.Registry
	compStore *compstore.ComponentStore
	meta      *meta.Meta

	id string
}

func (p *pubsub) init(ctx context.Context, comp compapi.Component) error {
	fName := comp.LogName()
	pubSub, err := p.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	baseMetadata := p.meta.ToBaseMetadata(comp)
	properties := baseMetadata.Properties
	consumerID := strings.TrimSpace(properties["consumerID"])
	if consumerID == "" {
		consumerID = p.id
	}
	properties["consumerID"] = consumerID

	err = pubSub.Init(ctx, contribpubsub.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	pubsubName := comp.ObjectMeta.Name

	p.compStore.AddPubSub(pubsubName, compstore.PubsubItem{
		Component:           pubSub,
		ScopedSubscriptions: scopes.GetScopedTopics(scopes.SubscriptionScopes, p.id, properties),
		ScopedPublishings:   scopes.GetScopedTopics(scopes.PublishingScopes, p.id, properties),
		AllowedTopics:       scopes.GetAllowedTopics(properties),
		ProtectedTopics:     scopes.GetProtectedTopics(properties),
		NamespaceScoped:     meta.ContainsNamespace(comp.Spec.Metadata),
	})
	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}
