package pubsub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/golang/protobuf/ptypes/empty"
)

const (
	getTopicsError         = "error getting topic list from app: %s"
	deserializeTopicsError = "error getting topics from app: %s"
	noSubscriptionsError   = "user app did not subscribe to any topic"
)

func GetSubscriptionsHTTP(channel channel.AppChannel, log logger.Logger) []Subscription {
	var subscriptions []Subscription

	req := invokev1.NewInvokeMethodRequest("dapr/subscribe")
	req.WithHTTPExtension(http.MethodGet, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate Context
	ctx := context.Background()
	resp, err := channel.InvokeMethod(ctx, req)
	if err != nil {
		log.Errorf(getTopicsError, err)
	}

	switch resp.Status().Code {
	case http.StatusOK:
		_, body := resp.RawData()
		if err := json.Unmarshal(body, &subscriptions); err != nil {
			log.Errorf(deserializeTopicsError, err)
		}
	case http.StatusNotFound:
		log.Debug(noSubscriptionsError)

	default:
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf("app returned http status code %v from subscription endpoint", resp.Status().Code)
	}

	log.Debugf("app responded with subscriptions %v", subscriptions)
	return filterSubscriptions(subscriptions, log)
}

func filterSubscriptions(subscriptions []Subscription, log logger.Logger) []Subscription {
	for i := len(subscriptions) - 1; i >= 0; i-- {
		if subscriptions[i].Route == "" {
			log.Warnf("topic %s has an empty route. removing from subscriptions list", subscriptions[i].Topic)
			subscriptions = append(subscriptions[:i], subscriptions[i+1:]...)
		}
	}
	return subscriptions
}

func GetSubscriptionsGRPC(channel runtimev1pb.AppCallbackClient, log logger.Logger) []Subscription {
	var subscriptions []Subscription

	resp, err := channel.ListTopicSubscriptions(context.Background(), &empty.Empty{})
	if err != nil {
		// Unexpected response: both GRPC and HTTP have to log the same level.
		log.Errorf(getTopicsError, err)
	} else {
		if resp == nil || resp.Subscriptions == nil || len(resp.Subscriptions) == 0 {
			log.Debug(noSubscriptionsError)
		} else {
			for _, s := range resp.Subscriptions {
				subscriptions = append(subscriptions, Subscription{
					PubsubName: s.PubsubName,
					Topic:      s.GetTopic(),
					Metadata:   s.GetMetadata(),
				})
			}
		}
	}
	return subscriptions
}
