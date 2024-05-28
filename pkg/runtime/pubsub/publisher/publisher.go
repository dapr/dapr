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

package publisher

import (
	"context"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

type GetPubSubFn func(name string) (*rtpubsub.PubsubItem, bool)

type Options struct {
	AppID       string
	Namespace   string
	Resiliency  resiliency.Provider
	GetPubSubFn GetPubSubFn
}

type publisher struct {
	appID       string
	namespace   string
	resiliency  resiliency.Provider
	getpubsubFn GetPubSubFn
}

var log = logger.NewLogger("dapr.runtime.pubsub.publisher")

func New(opts Options) rtpubsub.Adapter {
	return &publisher{
		appID:       opts.AppID,
		namespace:   opts.Namespace,
		resiliency:  opts.Resiliency,
		getpubsubFn: opts.GetPubSubFn,
	}
}

// Publish is an adapter method for the runtime to pre-validate publish requests
// And then forward them to the Pub/Sub component.
// This method is used by the HTTP and gRPC APIs.
func (p *publisher) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	pubsub, ok := p.getpubsubFn(req.PubsubName)
	if !ok {
		return rtpubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := rtpubsub.IsOperationAllowed(req.Topic, pubsub, pubsub.ScopedPublishings); !allowed {
		return rtpubsub.NotAllowedError{Topic: req.Topic, ID: p.appID}
	}

	if pubsub.NamespaceScoped {
		req.Topic = p.namespace + req.Topic
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		p.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub),
	)
	_, err := policyRunner(func(ctx context.Context) (any, error) {
		return nil, pubsub.Component.Publish(ctx, req)
	})
	return err
}

func (p *publisher) BulkPublish(ctx context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	pubsub, ok := p.getpubsubFn(req.PubsubName)
	if !ok {
		return contribpubsub.BulkPublishResponse{}, rtpubsub.NotFoundError{PubsubName: req.PubsubName}
	}

	if allowed := rtpubsub.IsOperationAllowed(req.Topic, pubsub, pubsub.ScopedPublishings); !allowed {
		return contribpubsub.BulkPublishResponse{}, rtpubsub.NotAllowedError{Topic: req.Topic, ID: p.appID}
	}

	policyDef := p.resiliency.ComponentOutboundPolicy(req.PubsubName, resiliency.Pubsub)

	if contribpubsub.FeatureBulkPublish.IsPresent(pubsub.Component.Features()) {
		return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, pubsub.Component.(contribpubsub.BulkPublisher))
	}

	log.Debugf("pubsub %s does not implement the BulkPublish API; falling back to publishing messages individually", req.PubsubName)
	defaultBulkPublisher := rtpubsub.NewDefaultBulkPublisher(pubsub.Component)

	return rtpubsub.ApplyBulkPublishResiliency(ctx, req, policyDef, defaultBulkPublisher)
}
