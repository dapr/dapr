package pubsub

import (
	"context"
	contrib_metadata "github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/pubsub"
	pubsub_middleware "github.com/dapr/dapr/pkg/middleware/pubsub"
	"github.com/dapr/kit/logger"
	jsoniter "github.com/json-iterator/go"
	"go.opencensus.io/trace"
)

const (
	pubsubName = "pubsubName"
)

type Route struct {
	path     string
	metadata map[string]string
}

type TopicRoute struct {
	routes map[string]Route
}

type SubscribedMessage struct {
	CloudEvent map[string]interface{}
	Data       []byte
	Topic      string
	Metadata   map[string]string
}

type SubscriptionHandler func(ctx context.Context, msg *SubscribedMessage) error

var log = logger.NewLogger("dapr.runtime.subscriberService")

type SubscriberService struct {
	Pipeline    pubsub_middleware.Pipeline
	topicRoutes map[string]TopicRoute
	Json        jsoniter.API

	PublishMessageFunc              SubscriptionHandler
	GetSubscriptionsFunc            func() []Subscription
	GetDeclarativeSubscriptionsFunc func() []Subscription
	OperationAccessValidatorFunc    func(pubsubName string, topic string) bool

	TracingFunc func(ctx context.Context, traceID string, spanName string) (context.Context, *trace.Span)
}

func (service *SubscriberService) StartSubscribing(pubSubs map[string]pubsub.PubSub) {
	for name, ps := range pubSubs {
		if err := service.beginPubSub(name, ps); err != nil {
			log.Errorf("error occurred while beginning pubsub %s: %s", name, err)
		}
	}
}

func (service *SubscriberService) beginPubSub(name string, ps pubsub.PubSub) error {
	topicRoutes, err := service.getTopicRoutes()
	if err != nil {
		return err
	}
	v, ok := topicRoutes[name]
	if !ok {
		return nil
	}
	for topic, route := range v.routes {
		allowed := service.OperationAccessValidatorFunc(name, topic)
		if !allowed {
			log.Warnf("subscription to topic %s on pubsub %s is not allowed", topic, name)
			continue
		}

		log.Debugf("subscribing to topic=%s on pubsub=%s", topic, name)

		routeMetadata := route.metadata
		if err := ps.Subscribe(pubsub.SubscribeRequest{
			Topic:    topic,
			Metadata: route.metadata,
		}, func(ctx context.Context, msg *pubsub.NewMessage) error {
			if msg.Metadata == nil {
				msg.Metadata = make(map[string]string, 1)
			}

			msg.Metadata[pubsubName] = name

			rawPayload, err := contrib_metadata.IsRawPayload(routeMetadata)
			if err != nil {
				log.Errorf("error deserializing pubsub metadata: %s", err)
				return err
			}

			var cloudEvent map[string]interface{}
			data := msg.Data
			if rawPayload {
				cloudEvent = pubsub.FromRawPayload(msg.Data, msg.Topic, name)
				data, err = service.Json.Marshal(cloudEvent)
				if err != nil {
					log.Errorf("error serializing cloud event in pubsub %s and topic %s: %s", name, msg.Topic, err)
					return err
				}
			} else {
				err = service.Json.Unmarshal(msg.Data, &cloudEvent)
				if err != nil {
					log.Errorf("error deserializing cloud event in pubsub %s and topic %s: %s", name, msg.Topic, err)
					return err
				}
			}

			return service.PublishMessageFunc(ctx, &SubscribedMessage{
				cloudEvent,
				data,
				msg.Topic,
				msg.Metadata,
			})
		}); err != nil {
			log.Errorf("failed to subscribe to topic %s: %s", topic, err)
		}
	}

	return nil
}

func (service *SubscriberService) getTopicRoutes() (map[string]TopicRoute, error) {
	if service.topicRoutes != nil {
		return service.topicRoutes, nil
	}

	topicRoutes := make(map[string]TopicRoute)

	// handle app subscriptions
	subscriptions := service.GetSubscriptionsFunc()

	// handle declarative subscriptions
	ds := service.GetDeclarativeSubscriptionsFunc()
	for _, s := range ds {
		skip := false

		// don't register duplicate subscriptions
		for _, sub := range subscriptions {
			if sub.Route == s.Route && sub.PubsubName == s.PubsubName && sub.Topic == s.Topic {
				log.Warnf("two identical subscriptions found (sources: declarative, app endpoint). topic: %s, route: %s, pubsubname: %s",
					s.Topic, s.Route, s.PubsubName)
				skip = true
				break
			}
		}

		if !skip {
			subscriptions = append(subscriptions, s)
		}
	}

	for _, s := range subscriptions {
		if _, ok := topicRoutes[s.PubsubName]; !ok {
			topicRoutes[s.PubsubName] = TopicRoute{routes: make(map[string]Route)}
		}

		topicRoutes[s.PubsubName].routes[s.Topic] = Route{path: s.Route, metadata: s.Metadata}
	}

	if len(topicRoutes) > 0 {
		for pubsubName, v := range topicRoutes {
			topics := []string{}
			for topic := range v.routes {
				topics = append(topics, topic)
			}
			log.Infof("app is subscribed to the following topics: %v through pubsub=%s", topics, pubsubName)
		}
	}
	service.topicRoutes = topicRoutes
	return topicRoutes, nil
}
