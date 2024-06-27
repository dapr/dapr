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

package pubsub

import (
	"context"
	"strings"
	"sync"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	comppubsub "github.com/dapr/dapr/pkg/components/pubsub"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/subscriber"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/scopes"
)

type Options struct {
	AppID          string
	Registry       *comppubsub.Registry
	Meta           *meta.Meta
	ComponentStore *compstore.ComponentStore
	Subscriber     *subscriber.Subscriber
}

type pubsub struct {
	appID      string
	registry   *comppubsub.Registry
	meta       *meta.Meta
	compStore  *compstore.ComponentStore
	subscriber *subscriber.Subscriber

	lock sync.RWMutex
}

func New(opts Options) *pubsub {
	return &pubsub{
		appID:      opts.AppID,
		registry:   opts.Registry,
		meta:       opts.Meta,
		compStore:  opts.ComponentStore,
		subscriber: opts.Subscriber,
	}
}

func (p *pubsub) Init(ctx context.Context, comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	fName := comp.LogName()
	pubSub, err := p.registry.Create(comp.Spec.Type, comp.Spec.Version, fName)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "creation", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.CreateComponentFailure, fName, err)
	}

	baseMetadata, err := p.meta.ToBaseMetadata(comp)
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	properties := baseMetadata.Properties
	consumerID := strings.TrimSpace(properties["consumerID"])
	if consumerID == "" {
		consumerID = p.appID
	}
	properties["consumerID"] = consumerID

	err = pubSub.Init(ctx, contribpubsub.Metadata{Base: baseMetadata})
	if err != nil {
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	pubsubName := comp.ObjectMeta.Name
	pubsubItem := &rtpubsub.PubsubItem{
		Component:           pubSub,
		ScopedSubscriptions: scopes.GetScopedTopics(scopes.SubscriptionScopes, p.appID, properties),
		ScopedPublishings:   scopes.GetScopedTopics(scopes.PublishingScopes, p.appID, properties),
		AllowedTopics:       scopes.GetAllowedTopics(properties),
		ProtectedTopics:     scopes.GetProtectedTopics(properties),
		NamespaceScoped:     meta.ContainsNamespace(comp.Spec.Metadata),
	}

	p.compStore.AddPubSub(pubsubName, pubsubItem)
	if err := p.subscriber.ReloadPubSub(pubsubName); err != nil {
		p.compStore.DeletePubSub(pubsubName)
		diag.DefaultMonitoring.ComponentInitFailed(comp.Spec.Type, "init", comp.ObjectMeta.Name)
		return rterrors.NewInit(rterrors.InitComponentFailure, fName, err)
	}

	diag.DefaultMonitoring.ComponentInitialized(comp.Spec.Type)

	return nil
}

func (p *pubsub) Close(comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.subscriber.StopPubSub(comp.Name)

	ps, ok := p.compStore.GetPubSub(comp.Name)
	if !ok {
		return nil
	}

	defer p.compStore.DeletePubSub(comp.Name)

	if err := ps.Component.Close(); err != nil {
		return err
	}

	return nil
}
