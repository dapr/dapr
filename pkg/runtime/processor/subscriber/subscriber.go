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

package subscriber

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription"
	"github.com/dapr/kit/logger"
)

type Options struct {
	AppID           string
	Namespace       string
	Resiliency      resiliency.Provider
	TracingSpec     *config.TracingSpec
	IsHTTP          bool
	Channels        *channels.Channels
	GRPC            *manager.Manager
	CompStore       *compstore.ComponentStore
	Adapter         rtpubsub.Adapter
	AdapterStreamer rtpubsub.AdapterStreamer
}

type Subscriber struct {
	appID           string
	namespace       string
	resiliency      resiliency.Provider
	tracingSpec     *config.TracingSpec
	isHTTP          bool
	channels        *channels.Channels
	grpc            *manager.Manager
	compStore       *compstore.ComponentStore
	adapter         rtpubsub.Adapter
	adapterStreamer rtpubsub.AdapterStreamer

	appSubs      map[string][]*namedSubscription
	streamSubs   map[string][]*namedSubscription
	appSubActive bool
	hasInitProg  bool
	lock         sync.RWMutex
	running      atomic.Bool
	closed       bool
}

type namedSubscription struct {
	name *string
	*subscription.Subscription
}

var log = logger.NewLogger("dapr.runtime.processor.subscription")

func New(opts Options) *Subscriber {
	return &Subscriber{
		appID:           opts.AppID,
		namespace:       opts.Namespace,
		resiliency:      opts.Resiliency,
		tracingSpec:     opts.TracingSpec,
		isHTTP:          opts.IsHTTP,
		channels:        opts.Channels,
		grpc:            opts.GRPC,
		compStore:       opts.CompStore,
		adapter:         opts.Adapter,
		adapterStreamer: opts.AdapterStreamer,
		appSubs:         make(map[string][]*namedSubscription),
		streamSubs:      make(map[string][]*namedSubscription),
	}
}

func (s *Subscriber) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("subscriber is already running")
	}

	<-ctx.Done()

	s.StopAllSubscriptionsForever()

	return nil
}

func (s *Subscriber) ReloadPubSub(name string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.closed {
		return nil
	}

	ps, _ := s.compStore.GetPubSub(name)

	var errs []error
	if err := s.reloadPubSubStream(name, ps); err != nil {
		errs = append(errs, fmt.Errorf("failed to reload pubsub for subscription streams %s: %s", name, err))
	}

	if err := s.reloadPubSubApp(name, ps); err != nil {
		errs = append(errs, fmt.Errorf("failed to reload pubsub for subscription apps %s: %s", name, err))
	}

	return errors.Join(errs...)
}

func (s *Subscriber) StartStreamerSubscription(key string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.closed {
		return apierrors.PubSub("").WithMetadata(nil).DeserializeError(errors.New("subscriber is closed"))
	}

	sub, ok := s.compStore.GetStreamSubscription(key)
	if !ok {
		err := fmt.Errorf("starting stream subscription without connection: %s", key)
		return apierrors.PubSub("").WithMetadata(nil).DeserializeError(err)
	}

	pubsub, ok := s.compStore.GetPubSub(sub.PubsubName)
	if !ok {
		return apierrors.PubSub(sub.PubsubName).WithMetadata(nil).NotFound()
	}

	ss, err := s.startSubscription(pubsub, sub, true)
	if err != nil {
		return fmt.Errorf("failed to create subscription for %s: %s", sub.PubsubName, err)
	}

	s.streamSubs[sub.PubsubName] = append(s.streamSubs[sub.PubsubName], &namedSubscription{
		name:         &key,
		Subscription: ss,
	})

	return nil
}

func (s *Subscriber) StopStreamerSubscription(pubsubName, key string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.closed {
		return
	}

	for i, sub := range s.streamSubs[pubsubName] {
		if sub.name != nil && *sub.name == key {
			sub.Stop()
			s.streamSubs[pubsubName] = append(s.streamSubs[pubsubName][:i], s.streamSubs[pubsubName][i+1:]...)
			return
		}
	}
}

func (s *Subscriber) ReloadDeclaredAppSubscription(name, pubsubName string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if !s.appSubActive || s.closed {
		return nil
	}

	for i, appsub := range s.appSubs[pubsubName] {
		if appsub.name != nil && name == *appsub.name {
			appsub.Stop()
			s.appSubs[pubsubName] = append(s.appSubs[pubsubName][:i], s.appSubs[pubsubName][i+1:]...)
			break
		}
	}

	ps, ok := s.compStore.GetPubSub(pubsubName)
	if !ok {
		return nil
	}

	sub, ok := s.compStore.GetDeclarativeSubscription(name)
	if !ok {
		return nil
	}

	if !rtpubsub.IsOperationAllowed(sub.Topic, ps, ps.ScopedSubscriptions) {
		return nil
	}

	ss, err := s.startSubscription(ps, sub.NamedSubscription, false)
	if err != nil {
		return fmt.Errorf("failed to create subscription for %s: %s", sub.PubsubName, err)
	}

	s.appSubs[sub.PubsubName] = append(s.appSubs[sub.PubsubName], &namedSubscription{
		name:         &name,
		Subscription: ss,
	})

	return nil
}

func (s *Subscriber) StopPubSub(name string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	for _, sub := range s.appSubs[name] {
		sub.Stop()
	}
	for _, sub := range s.streamSubs[name] {
		sub.Stop()
	}

	s.appSubs[name] = nil
	s.streamSubs[name] = nil
}

func (s *Subscriber) StartAppSubscriptions() error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.appSubActive || s.closed {
		return nil
	}

	if err := s.initProgramaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	s.appSubActive = true

	for _, subs := range s.appSubs {
		for _, sub := range subs {
			sub.Stop()
		}
	}
	s.appSubs = make(map[string][]*namedSubscription)

	var errs []error
	for name, ps := range s.compStore.ListPubSubs() {
		for _, sub := range s.compStore.ListSubscriptionsAppByPubSub(name) {
			ss, err := s.startSubscription(ps, sub, false)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			s.appSubs[name] = append(s.appSubs[name], &namedSubscription{
				name:         sub.Name,
				Subscription: ss,
			})
		}
	}

	return errors.Join(errs...)
}

func (s *Subscriber) StopAppSubscriptions() {
	s.lock.Lock()
	defer s.lock.Unlock()

	if !s.appSubActive {
		return
	}

	s.appSubActive = false

	for _, psub := range s.appSubs {
		for _, sub := range psub {
			sub.Stop()
		}
	}

	s.appSubs = make(map[string][]*namedSubscription)
}

func (s *Subscriber) StopAllSubscriptionsForever() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.closed = true

	for _, psubs := range s.appSubs {
		for _, sub := range psubs {
			sub.Stop()
		}
	}
	for _, psubs := range s.streamSubs {
		for _, sub := range psubs {
			sub.Stop()
		}
	}

	s.appSubs = make(map[string][]*namedSubscription)
	s.streamSubs = make(map[string][]*namedSubscription)
}

func (s *Subscriber) InitProgramaticSubscriptions(ctx context.Context) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.initProgramaticSubscriptions(ctx)
}

func (s *Subscriber) reloadPubSubStream(name string, pubsub *rtpubsub.PubsubItem) error {
	for _, sub := range s.streamSubs[name] {
		sub.Stop()
	}
	s.streamSubs[name] = nil

	if s.closed || pubsub == nil {
		return nil
	}

	subs := make([]*namedSubscription, 0, len(s.compStore.ListSubscriptionsStreamByPubSub(name)))
	var errs []error
	for _, sub := range s.compStore.ListSubscriptionsStreamByPubSub(name) {
		ss, err := s.startSubscription(pubsub, sub, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to create subscription for %s: %s", name, err))
			continue
		}

		subs = append(subs, &namedSubscription{
			name:         sub.Name,
			Subscription: ss,
		})
	}

	s.streamSubs[name] = subs

	return errors.Join(errs...)
}

func (s *Subscriber) reloadPubSubApp(name string, pubsub *rtpubsub.PubsubItem) error {
	for _, sub := range s.appSubs[name] {
		sub.Stop()
	}

	s.appSubs[name] = nil

	if !s.appSubActive || s.closed || pubsub == nil {
		return nil
	}

	if err := s.initProgramaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	var errs []error
	subs := make([]*namedSubscription, 0, len(s.compStore.ListSubscriptionsAppByPubSub(name)))
	for _, sub := range s.compStore.ListSubscriptionsAppByPubSub(name) {
		ss, err := s.startSubscription(pubsub, sub, false)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to create subscription for %s: %s", name, err))
			continue
		}

		subs = append(subs, &namedSubscription{
			name:         sub.Name,
			Subscription: ss,
		})
	}

	s.appSubs[name] = subs

	return errors.Join(errs...)
}

func (s *Subscriber) initProgramaticSubscriptions(ctx context.Context) error {
	if s.hasInitProg {
		return nil
	}

	// If no pubsubs registered, return early.
	if len(s.compStore.ListPubSubs()) == 0 {
		return nil
	}

	appChannel := s.channels.AppChannel()
	if appChannel == nil {
		log.Warn("app channel not initialized, make sure -app-port is specified if pubsub subscription is required")
		return nil
	}

	s.hasInitProg = true

	var (
		subscriptions []rtpubsub.Subscription
		err           error
	)

	// handle app subscriptions
	if s.isHTTP {
		subscriptions, err = rtpubsub.GetSubscriptionsHTTP(ctx, appChannel, log, s.resiliency)
	} else {
		var conn grpc.ClientConnInterface
		conn, err = s.grpc.GetAppClient()
		if err != nil {
			return fmt.Errorf("error while getting app client: %w", err)
		}
		client := runtimev1pb.NewAppCallbackClient(conn)
		subscriptions, err = rtpubsub.GetSubscriptionsGRPC(ctx, client, log, s.resiliency)
	}
	if err != nil {
		return err
	}

	subbedTopics := make(map[string][]string)
	for _, sub := range subscriptions {
		subbedTopics[sub.PubsubName] = append(subbedTopics[sub.PubsubName], sub.Topic)
	}
	for pubsubName, topics := range subbedTopics {
		log.Infof("app is subscribed to the following topics: [%s] through pubsub=%s", topics, pubsubName)
	}

	s.compStore.SetProgramaticSubscriptions(subscriptions...)

	return nil
}

func (s *Subscriber) startSubscription(pubsub *rtpubsub.PubsubItem, comp *compstore.NamedSubscription, isStreamer bool) (*subscription.Subscription, error) {
	var streamer rtpubsub.AdapterStreamer
	if isStreamer {
		streamer = s.adapterStreamer
	}
	return subscription.New(subscription.Options{
		AppID:           s.appID,
		Namespace:       s.namespace,
		PubSubName:      comp.PubsubName,
		Topic:           comp.Topic,
		IsHTTP:          s.isHTTP,
		PubSub:          pubsub,
		Resiliency:      s.resiliency,
		TraceSpec:       s.tracingSpec,
		Route:           comp.Subscription,
		Channels:        s.channels,
		GRPC:            s.grpc,
		Adapter:         s.adapter,
		AdapterStreamer: streamer,
	})
}
