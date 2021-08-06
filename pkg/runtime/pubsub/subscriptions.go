package pubsub

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ghodss/yaml"
	"google.golang.org/protobuf/types/known/emptypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/logger"
)

const (
	getTopicsError         = "error getting topic list from app: %s"
	deserializeTopicsError = "error getting topics from app: %s"
	noSubscriptionsError   = "user app did not subscribe to any topic"
	subscriptionKind       = "Subscription"
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

	resp, err := channel.ListTopicSubscriptions(context.Background(), &emptypb.Empty{})
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

// DeclarativeSelfHosted loads subscriptions from the given components path.
func DeclarativeSelfHosted(componentsPath string, log logger.Logger) []Subscription {
	var subs []Subscription

	if _, err := os.Stat(componentsPath); os.IsNotExist(err) {
		return subs
	}

	files, err := ioutil.ReadDir(componentsPath)
	if err != nil {
		log.Errorf("failed to read subscriptions from path %s: %s", err)
		return subs
	}

	for _, f := range files {
		if !f.IsDir() {
			filePath := filepath.Join(componentsPath, f.Name())
			b, err := ioutil.ReadFile(filePath)
			if err != nil {
				log.Errorf("failed to read file %s: %s", filePath, err)
				continue
			}

			subs, err = appendSubscription(subs, b)
			if err != nil {
				log.Warnf("failed to add subscription from file %s: %s", filePath, err)
				continue
			}
		}
	}
	return subs
}

func marshalSubscription(b []byte) (*Subscription, error) {
	// Parse only the type metadata first in order
	// to filter out non-Subscriptions without other errors.
	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	var ti typeInfo
	err := yaml.Unmarshal(b, &ti)
	if err != nil {
		return nil, err
	}

	if ti.Kind != subscriptionKind {
		return nil, nil
	}

	var sub subscriptionsapi.Subscription
	err = yaml.Unmarshal(b, &sub)
	if err != nil {
		return nil, err
	}

	return &Subscription{
		Topic:      sub.Spec.Topic,
		PubsubName: sub.Spec.Pubsubname,
		Route:      sub.Spec.Route,
		Metadata:   sub.Spec.Metadata,
		Scopes:     sub.Scopes,
	}, nil
}

// DeclarativeKubernetes loads subscriptions from the operator when running in Kubernetes.
func DeclarativeKubernetes(client operatorv1pb.OperatorClient, log logger.Logger) []Subscription {
	var subs []Subscription
	resp, err := client.ListSubscriptions(context.TODO(), &emptypb.Empty{})
	if err != nil {
		log.Errorf("failed to list subscriptions from operator: %s", err)
		return subs
	}

	for _, s := range resp.Subscriptions {
		subs, err = appendSubscription(subs, s)
		if err != nil {
			log.Warnf("failed to add subscription from operator: %s", err)
			continue
		}
	}
	return subs
}

func appendSubscription(list []Subscription, subBytes []byte) ([]Subscription, error) {
	sub, err := marshalSubscription(subBytes)
	if err != nil {
		return nil, err
	}

	if sub != nil {
		list = append(list, *sub)
	}
	return list, nil
}
