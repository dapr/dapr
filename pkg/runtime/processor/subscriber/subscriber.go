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

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/pubsub"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman"
	postmangrpc "github.com/dapr/dapr/pkg/runtime/subscription/postman/grpc"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/http"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/streaming"
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
	streamSubs   map[string]map[rtpubsub.ConnectionID]*namedSubscription
	appSubActive bool
	hasInitProg  bool
	lock         sync.RWMutex
	closed       atomic.Bool

	retryCtx    map[string]context.Context
	retryCancel map[string]context.CancelFunc
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
		streamSubs:      make(map[string]map[rtpubsub.ConnectionID]*namedSubscription),
		retryCtx:        make(map[string]context.Context),
		retryCancel:     make(map[string]context.CancelFunc),
	}
}

func (s *Subscriber) Run(ctx context.Context) error {
	<-ctx.Done()
	s.closed.Store(true)
	s.cancelAllRetries()
	return nil
}

func (s *Subscriber) StopAllSubscriptionsForever() {
	s.lock.Lock()

	s.closed.Store(true)
	s.cancelAllRetries()

	var wg sync.WaitGroup
	for _, psubs := range s.appSubs {
		wg.Add(len(psubs))
		for _, sub := range psubs {
			go func(sub *namedSubscription) {
				sub.Stop(pubsub.ErrGracefulShutdown)
				wg.Done()
			}(sub)
		}
	}

	for _, psubs := range s.streamSubs {
		wg.Add(len(psubs))
		for _, sub := range psubs {
			go func() {
				sub.Stop(pubsub.ErrGracefulShutdown)
				wg.Done()
			}()
		}
	}

	s.appSubs = make(map[string][]*namedSubscription)
	s.streamSubs = make(map[string]map[rtpubsub.ConnectionID]*namedSubscription)
	s.lock.Unlock()

	wg.Wait()
}

func (s *Subscriber) ReloadPubSub(name string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.closed.Load() {
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

func (s *Subscriber) StartStreamerSubscription(subscription *subapi.Subscription, connectionID rtpubsub.ConnectionID) error {
	s.lock.Lock()
	defer func() {
		s.lock.Unlock()
	}()

	if s.closed.Load() {
		return apierrors.PubSub("").WithMetadata(nil).DeserializeError(errors.New("subscriber is closed"))
	}

	sub, found := s.compStore.GetStreamSubscription(subscription)
	if !found {
		return fmt.Errorf("streaming subscription %s not found", subscription.Name)
	}

	pubsub, ok := s.compStore.GetPubSub(sub.PubsubName)
	if !ok {
		return apierrors.PubSub(sub.PubsubName).WithMetadata(nil).NotFound()
	}

	ss, err := s.startSubscription(pubsub, sub, true)
	if err != nil {
		return fmt.Errorf("failed to create subscription for %s: %s", sub.PubsubName, err)
	}

	key := s.adapterStreamer.StreamerKey(sub.PubsubName, sub.Topic)

	_, exists := s.streamSubs[sub.PubsubName]
	if !exists {
		s.streamSubs[sub.PubsubName] = make(map[rtpubsub.ConnectionID]*namedSubscription)
	}

	s.streamSubs[sub.PubsubName][connectionID] = &namedSubscription{
		name:         &key,
		Subscription: ss,
	}
	return nil
}

func (s *Subscriber) StopStreamerSubscription(subscription *subapi.Subscription, connectionID rtpubsub.ConnectionID) {
	s.lock.Lock()
	defer func() {
		s.lock.Unlock()
	}()

	subscriptions, ok := s.streamSubs[subscription.Spec.Pubsubname]
	if !ok {
		return
	}

	sub, ok := subscriptions[connectionID]
	if !ok {
		return
	}

	sub.Stop()
	delete(subscriptions, connectionID)
	if len(subscriptions) == 0 {
		delete(s.streamSubs, subscription.Spec.Pubsubname)
	}
}

func (s *Subscriber) ReloadDeclaredAppSubscription(name, pubsubName string) error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if !s.appSubActive || s.closed.Load() {
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
		log.Errorf("Failed to start declared subscription %s for pubsub %s, topic %s: %s", name, pubsubName, sub.Topic, err)
		go s.retrySubscription(pubsubName, ps, sub.NamedSubscription)

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

	s.cancelRetries(name)

	var wg sync.WaitGroup
	wg.Add(len(s.appSubs[name]) + len(s.streamSubs[name]))
	for _, sub := range s.appSubs[name] {
		go func(sub *namedSubscription) {
			sub.Stop()
			wg.Done()
		}(sub)
	}
	for _, sub := range s.streamSubs[name] {
		go func(sub *namedSubscription) {
			sub.Stop()
			wg.Done()
		}(sub)
	}
	wg.Wait()

	s.appSubs[name] = nil
	s.streamSubs[name] = nil
}

func (s *Subscriber) StartAppSubscriptions() error {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.appSubActive || s.closed.Load() {
		return nil
	}

	if err := s.initProgrammaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	s.appSubActive = true

	var wg sync.WaitGroup
	for _, subs := range s.appSubs {
		wg.Add(len(subs))
		for _, sub := range subs {
			go func(sub *namedSubscription) {
				sub.Stop()
				wg.Done()
			}(sub)
		}
	}

	wg.Wait()

	s.appSubs = make(map[string][]*namedSubscription)

	var errs []error

	for name, ps := range s.compStore.ListPubSubs() {
		for _, sub := range s.compStore.ListSubscriptionsAppByPubSub(name) {
			ss, err := s.startSubscription(ps, sub, false)
			if err != nil {
				errs = append(errs, err)
				log.Errorf("Failed to start subscription for pubsub %s, topic %s: %s", name, sub.Topic, err)

				go s.retrySubscription(name, ps, sub)
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

func (s *Subscriber) retrySubscription(pubsubName string, pubsub *rtpubsub.PubsubItem, sub *compstore.NamedSubscription) {
	s.lock.Lock()
	ctx := s.retryCtx[pubsubName]
	if s.retryCtx[pubsubName] == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		s.retryCtx[pubsubName] = ctx
		s.retryCancel[pubsubName] = cancel
	}
	s.lock.Unlock()

	backoff.Retry(func() error {
		if s.closed.Load() {
			log.Debugf("Stopping retry for subscription pubsub %s, topic %s due to subscriber closure", pubsubName, sub.Topic)
			return nil
		}

		ss, err := s.startSubscription(pubsub, sub, false)
		if err != nil {
			log.Errorf("Retry failed for subscription pubsub %s, topic %s: %s. Will retry.", pubsubName, sub.Topic, err)
			return err
		}

		s.lock.Lock()
		if !s.closed.Load() && s.appSubActive {
			s.appSubs[pubsubName] = append(s.appSubs[pubsubName], &namedSubscription{
				name:         sub.Name,
				Subscription: ss,
			})
			log.Infof("Successfully started subscription after retry for pubsub %s, topic %s", pubsubName, sub.Topic)
		} else {
			// Subscriber was closed while we were retrying, stop the subscription
			ss.Stop()
		}
		s.lock.Unlock()
		return nil
	}, backoff.WithContext(backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(0)), ctx))
}

func (s *Subscriber) StopAppSubscriptions() {
	s.lock.Lock()
	defer s.lock.Unlock()

	if !s.appSubActive {
		return
	}

	s.appSubActive = false
	s.cancelAllRetries()

	var wg sync.WaitGroup
	for _, psub := range s.appSubs {
		wg.Add(len(psub))
		for _, sub := range psub {
			go func(sub *namedSubscription) {
				sub.Stop()
				wg.Done()
			}(sub)
		}
	}
	wg.Wait()

	s.appSubs = make(map[string][]*namedSubscription)
}

func (s *Subscriber) InitProgramaticSubscriptions(ctx context.Context) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.initProgrammaticSubscriptions(ctx)
}

func (s *Subscriber) reloadPubSubStream(name string, pubsub *rtpubsub.PubsubItem) error {
	var wg sync.WaitGroup
	wg.Add(len(s.streamSubs[name]))
	for _, sub := range s.streamSubs[name] {
		go func(sub *namedSubscription) {
			sub.Stop()
			wg.Done()
		}(sub)
	}
	wg.Wait()

	s.streamSubs[name] = nil

	if s.closed.Load() || pubsub == nil {
		return nil
	}

	subs := make(map[rtpubsub.ConnectionID]*namedSubscription, len(s.compStore.ListSubscriptionsStreamByPubSub(name)))
	var errs []error
	for _, sub := range s.compStore.ListSubscriptionsStreamByPubSub(name) {
		ss, err := s.startSubscription(pubsub, sub, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to create subscription for %s: %s", name, err))
			continue
		}

		subs[sub.ConnectionID] = &namedSubscription{
			name:         sub.Name,
			Subscription: ss,
		}
	}

	s.streamSubs[name] = subs

	return errors.Join(errs...)
}

func (s *Subscriber) reloadPubSubApp(name string, pubsub *rtpubsub.PubsubItem) error {
	s.cancelRetries(name)

	var wg sync.WaitGroup
	wg.Add(len(s.appSubs[name]))
	for _, sub := range s.appSubs[name] {
		go func(sub *namedSubscription) {
			sub.Stop()
			wg.Done()
		}(sub)
	}
	wg.Wait()

	s.appSubs[name] = nil

	if !s.appSubActive || s.closed.Load() || pubsub == nil {
		return nil
	}

	if err := s.initProgrammaticSubscriptions(context.TODO()); err != nil {
		return err
	}

	var errs []error
	subs := make([]*namedSubscription, 0, len(s.compStore.ListSubscriptionsAppByPubSub(name)))

	for _, sub := range s.compStore.ListSubscriptionsAppByPubSub(name) {
		ss, err := s.startSubscription(pubsub, sub, false)
		if err != nil {
			log.Errorf("Failed to reload subscription for pubsub %s, topic %s: %s", name, sub.Topic, err)
			errs = append(errs, fmt.Errorf("failed to create subscription for %s: %s", name, err))

			go s.retrySubscription(name, pubsub, sub)
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

func (s *Subscriber) initProgrammaticSubscriptions(ctx context.Context) error {
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
		subscriptions, err = rtpubsub.GetSubscriptionsHTTP(ctx, appChannel, log, s.resiliency, s.appID)
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
	// TODO: @joshvanl
	var postman postman.Interface
	var streamer rtpubsub.AdapterStreamer
	if isStreamer {
		streamer = s.adapterStreamer
		postman = streaming.New(streaming.Options{
			Tracing: s.tracingSpec,
			Channel: s.adapterStreamer,
		})
	} else {
		if s.isHTTP {
			postman = http.New(http.Options{
				Channels: s.channels,
				Tracing:  s.tracingSpec,
				Adapter:  s.adapter,
			})
		} else {
			postman = postmangrpc.New(postmangrpc.Options{
				Channel: s.grpc,
				Tracing: s.tracingSpec,
				Adapter: s.adapter,
			})
		}
	}
	return subscription.New(subscription.Options{
		AppID:           s.appID,
		Namespace:       s.namespace,
		PubSubName:      comp.PubsubName,
		Topic:           comp.Topic,
		PubSub:          pubsub,
		Resiliency:      s.resiliency,
		TraceSpec:       s.tracingSpec,
		Route:           comp.Subscription,
		GRPC:            s.grpc,
		Adapter:         s.adapter,
		AdapterStreamer: streamer,
		ConnectionID:    comp.ConnectionID,
		Postman:         postman,
	})
}

func (s *Subscriber) cancelRetries(pubsubName string) {
	if cancel, exists := s.retryCancel[pubsubName]; exists {
		cancel()
		delete(s.retryCtx, pubsubName)
		delete(s.retryCancel, pubsubName)
	}
}

func (s *Subscriber) cancelAllRetries() {
	for _, cancel := range s.retryCancel {
		cancel()
	}
	s.retryCtx = make(map[string]context.Context)
	s.retryCancel = make(map[string]context.CancelFunc)
}
