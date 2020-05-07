package pubsub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/logger"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	daprclientv1pb "github.com/dapr/dapr/pkg/proto/daprclient/v1"
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
		log.Warnf("app returned http status code %v from subscription endpoint", resp.Status().Code)
	}
	for i := len(subscriptions) - 1; i >= 0; i-- {
		if subscriptions[i].Route == "" {
			subscriptions = append(subscriptions[:i], subscriptions[i+1:]...)
			log.Warnf("topic %s has an empty route. removing from subscriptions list", subscriptions[i].Topic)
		}
	}
	return subscriptions
}

func GetSubscriptionsGRPC(channel daprclientv1pb.DaprClientClient, log logger.Logger) []Subscription {
	var subscriptions []Subscription

	resp, err := channel.GetTopicSubscriptions(context.Background(), &empty.Empty{})
	if err != nil {
		log.Errorf(getTopicsError, err)
	} else {
		if resp == nil || resp.Subscriptions == nil || len(resp.Subscriptions) == 0 {
			log.Debug(noSubscriptionsError)
		} else {
			for _, s := range resp.Subscriptions {
				subscriptions = append(subscriptions, Subscription{
					Topic:    s.GetTopic(),
					Metadata: s.GetMetadata(),
				})
			}
		}
	}
	return subscriptions
}
