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

package pubsub

import (
	"context"
	"fmt"
	"strings"
	"sync"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	comppubsub "github.com/dapr/dapr/pkg/components/pubsub"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/outbox"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.pubsub")

type Options struct {
	ID        string
	Namespace string
	IsHTTP    bool
	PodName   string
	Mode      modes.DaprMode

	Registry       *comppubsub.Registry
	ComponentStore *compstore.ComponentStore
	Resiliency     resiliency.Provider
	Meta           *meta.Meta
	TracingSpec    *config.TracingSpec
	GRPC           *manager.Manager
	Channels       *channels.Channels
	OperatorClient operatorv1.OperatorClient
}

type pubsub struct {
	id          string
	namespace   string
	isHTTP      bool
	podName     string
	mode        modes.DaprMode
	tracingSpec *config.TracingSpec

	registry       *comppubsub.Registry
	resiliency     resiliency.Provider
	compStore      *compstore.ComponentStore
	meta           *meta.Meta
	grpc           *manager.Manager
	channels       *channels.Channels
	operatorClient operatorv1.OperatorClient
	streamer       *streamer

	lock        sync.RWMutex
	subscribing bool
	stopForever bool

	topicCancels map[string]context.CancelFunc
	outbox       outbox.Outbox
}

func New(opts Options) *pubsub {
	ps := &pubsub{
		id:             opts.ID,
		namespace:      opts.Namespace,
		isHTTP:         opts.IsHTTP,
		podName:        opts.PodName,
		mode:           opts.Mode,
		registry:       opts.Registry,
		resiliency:     opts.Resiliency,
		compStore:      opts.ComponentStore,
		meta:           opts.Meta,
		tracingSpec:    opts.TracingSpec,
		grpc:           opts.GRPC,
		channels:       opts.Channels,
		operatorClient: opts.OperatorClient,
		topicCancels:   make(map[string]context.CancelFunc),
	}

	ps.streamer = &streamer{
		pubsub:      ps,
		subscribers: make(map[string]*streamconn),
	}
	ps.outbox = rtpubsub.NewOutbox(ps.Publish, opts.ComponentStore.GetPubSubComponent, opts.ComponentStore.GetStateStore, rtpubsub.ExtractCloudEventProperty, opts.Namespace)
	return ps
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

	if p.subscribing {
		return p.beginPubSub(ctx, pubsubName)
	}

	return nil
}

func (p *pubsub) Close(comp compapi.Component) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	ps, ok := p.compStore.GetPubSub(comp.Name)
	if !ok {
		return nil
	}

	defer p.compStore.DeletePubSub(comp.Name)

	for topic := range p.compStore.GetTopicRoutes()[comp.Name] {
		subKey := topicKey(comp.Name, topic)
		p.unsubscribeTopic(subKey)
		p.compStore.DeleteTopicRoute(subKey)
	}

	if err := ps.Component.Close(); err != nil {
		return err
	}

	return nil
}

func (p *pubsub) Outbox() outbox.Outbox {
	return p.outbox
}

func (p *pubsub) Streamer() rtpubsub.Streamer {
	return p.streamer
}

// findMatchingRoute selects the path based on routing rules. If there are
// no matching rules, the route-level path is used.
func findMatchingRoute(rules []*rtpubsub.Rule, cloudEvent interface{}) (path string, shouldProcess bool, err error) {
	hasRules := len(rules) > 0
	if hasRules {
		data := map[string]interface{}{
			"event": cloudEvent,
		}
		rule, err := matchRoutingRule(rules, data)
		if err != nil {
			return "", false, err
		}
		if rule != nil {
			return rule.Path, true, nil
		}
	}

	return "", false, nil
}

func matchRoutingRule(rules []*rtpubsub.Rule, data map[string]interface{}) (*rtpubsub.Rule, error) {
	for _, rule := range rules {
		if rule.Match == nil || len(rule.Match.String()) == 0 {
			return rule, nil
		}
		iResult, err := rule.Match.Eval(data)
		if err != nil {
			return nil, err
		}
		result, ok := iResult.(bool)
		if !ok {
			return nil, fmt.Errorf("the result of match expression %s was not a boolean", rule.Match)
		}

		if result {
			return rule, nil
		}
	}

	return nil, nil
}
