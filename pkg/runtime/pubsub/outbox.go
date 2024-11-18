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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"

	"github.com/dapr/components-contrib/metadata"
	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/outbox"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/utils"
)

const (
	outboxPublishPubsubKey           = "outboxPublishPubsub"
	outboxPublishTopicKey            = "outboxPublishTopic"
	outboxPubsubKey                  = "outboxPubsub"
	outboxDiscardWhenMissingStateKey = "outboxDiscardWhenMissingState"
	outboxStatePrefix                = "outbox"
	defaultStateScanDelay            = time.Second * 1
)

var outboxLogger = logger.NewLogger("dapr.outbox")

type outboxConfig struct {
	publishPubSub                 string
	publishTopic                  string
	outboxPubsub                  string
	outboxDiscardWhenMissingState bool
}

type outboxImpl struct {
	cloudEventExtractorFn func(map[string]any, string) string
	getPubsubFn           func(string) (contribPubsub.PubSub, bool)
	getStateFn            func(string) (state.Store, bool)
	publisher             Adapter
	outboxStores          map[string]outboxConfig
	lock                  sync.RWMutex
	namespace             string
}

type OptionsOutbox struct {
	Publisher             Adapter
	GetPubsubFn           func(string) (contribPubsub.PubSub, bool)
	GetStateFn            func(string) (state.Store, bool)
	CloudEventExtractorFn func(map[string]any, string) string
	Namespace             string
}

// NewOutbox returns an instance of an Outbox.
func NewOutbox(opts OptionsOutbox) outbox.Outbox {
	return &outboxImpl{
		cloudEventExtractorFn: opts.CloudEventExtractorFn,
		getPubsubFn:           opts.GetPubsubFn,
		getStateFn:            opts.GetStateFn,
		publisher:             opts.Publisher,
		outboxStores:          make(map[string]outboxConfig),
		namespace:             opts.Namespace,
	}
}

// AddOrUpdateOutbox examines a statestore for outbox properties and saves it for later usage in outbox operations.
func (o *outboxImpl) AddOrUpdateOutbox(stateStore v1alpha1.Component) {
	var publishPubSub, publishTopicKey, outboxPubsub string
	var outboxDiscardWhenMissingState bool

	for _, v := range stateStore.Spec.Metadata {
		switch v.Name {
		case outboxPublishPubsubKey:
			publishPubSub = v.Value.String()
		case outboxPublishTopicKey:
			publishTopicKey = v.Value.String()
		case outboxPubsubKey:
			outboxPubsub = v.Value.String()
		case outboxDiscardWhenMissingStateKey:
			outboxDiscardWhenMissingState = utils.IsTruthy(v.Value.String())
		}
	}

	if publishPubSub != "" && publishTopicKey != "" {
		o.lock.Lock()
		defer o.lock.Unlock()

		if outboxPubsub == "" {
			outboxPubsub = publishPubSub
		}

		o.outboxStores[stateStore.Name] = outboxConfig{
			publishPubSub:                 publishPubSub,
			publishTopic:                  publishTopicKey,
			outboxPubsub:                  outboxPubsub,
			outboxDiscardWhenMissingState: outboxDiscardWhenMissingState,
		}
	}
}

// Enabled returns a bool to indicate if a state store has outbox configured
func (o *outboxImpl) Enabled(stateStore string) bool {
	o.lock.RLock()
	defer o.lock.RUnlock()

	_, ok := o.outboxStores[stateStore]
	return ok
}

func transaction() (state.TransactionalStateOperation, error) {
	uid, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	return state.SetRequest{
		Key:   outboxStatePrefix + "-" + uid.String(),
		Value: "0",
	}, nil
}

// PublishInternal publishes the state to an internal topic for outbox processing and returns the updated list of transactions
func (o *outboxImpl) PublishInternal(ctx context.Context, stateStore string, operations []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error) {
	o.lock.RLock()
	c, ok := o.outboxStores[stateStore]
	o.lock.RUnlock()

	if !ok {
		return nil, fmt.Errorf("error publishing internal outbox message: could not find outbox configuration on state store %s", stateStore)
	}

	projections := map[string]state.SetRequest{}

	for i, op := range operations {
		sr, ok := op.(state.SetRequest)

		if ok {
			for k, v := range sr.Metadata {
				if k == "outbox.projection" && utils.IsTruthy(v) {
					projections[sr.Key] = sr
					operations = append(operations[:i], operations[i+1:]...)
				}
			}
		}
	}

	for _, op := range operations {
		sr, ok := op.(state.SetRequest)
		if ok {
			tr, err := transaction()
			if err != nil {
				return nil, err
			}

			var payload any
			var contentType string

			if proj, ok := projections[sr.Key]; ok {
				payload = proj.Value

				if proj.ContentType != nil {
					contentType = *proj.ContentType
				} else if ct, ok := proj.Metadata[metadata.ContentType]; ok {
					contentType = ct
				}
			} else {
				payload = sr.Value

				if sr.ContentType != nil {
					contentType = *sr.ContentType
				} else if ct, ok := sr.Metadata[metadata.ContentType]; ok {
					contentType = ct
				}
			}

			var ceData []byte
			bt, ok := payload.([]byte)
			if ok {
				ceData = bt
			} else if contentType != "" && strings.EqualFold(contentType, "application/json") {
				b, sErr := json.Marshal(payload)
				if sErr != nil {
					return nil, sErr
				}

				ceData = b
			} else {
				ceData = []byte(fmt.Sprintf("%v", payload))
			}

			var dataContentType string
			if contentType != "" {
				dataContentType = contentType
			}

			ce := contribPubsub.NewCloudEventsEnvelope(tr.GetKey(), source, "", "", "", c.outboxPubsub, dataContentType, ceData, "", traceState)
			ce[contribPubsub.TraceIDField] = traceID

			for k, v := range op.GetMetadata() {
				if k == contribPubsub.DataField || k == contribPubsub.IDField {
					continue
				}

				ce[k] = v
			}

			data, err := json.Marshal(ce)
			if err != nil {
				return nil, err
			}

			err = o.publisher.Publish(ctx, &contribPubsub.PublishRequest{
				PubsubName: c.outboxPubsub,
				Data:       data,
				Topic:      outboxTopic(source, c.publishTopic, o.namespace),
			})
			if err != nil {
				return nil, err
			}

			operations = append(operations, tr)
		}
	}

	return operations, nil
}

func outboxTopic(appID, topic, namespace string) string {
	return namespace + appID + topic + "outbox"
}

func (o *outboxImpl) SubscribeToInternalTopics(ctx context.Context, appID string) error {
	o.lock.RLock()
	defer o.lock.RUnlock()

	for stateStore, c := range o.outboxStores {
		outboxPubsub, ok := o.getPubsubFn(c.outboxPubsub)
		if !ok {
			outboxLogger.Warnf("could not subscribe to internal outbox topic: outbox pubsub %s not loaded", c.outboxPubsub)
			continue
		}

		outboxPubsub.Subscribe(ctx, contribPubsub.SubscribeRequest{
			Topic: outboxTopic(appID, c.publishTopic, o.namespace),
		}, func(ctx context.Context, msg *contribPubsub.NewMessage) error {
			var cloudEvent map[string]interface{}

			err := json.Unmarshal(msg.Data, &cloudEvent)
			if err != nil {
				return err
			}

			stateKey := o.cloudEventExtractorFn(cloudEvent, contribPubsub.IDField)

			store, ok := o.getStateFn(stateStore)
			if !ok {
				return fmt.Errorf("cannot get outbox state: state store %s not found", stateStore)
			}

			time.Sleep(defaultStateScanDelay)

			bo := &backoff.ExponentialBackOff{
				InitialInterval:     time.Millisecond * 500,
				MaxInterval:         time.Second * 3,
				MaxElapsedTime:      time.Second * 10,
				Multiplier:          3,
				Clock:               backoff.SystemClock,
				RandomizationFactor: 0.1,
			}

			err = backoff.Retry(func() error {
				resp, sErr := store.Get(ctx, &state.GetRequest{
					Key: stateKey,
				})
				if sErr != nil {
					return sErr
				}

				if resp != nil && len(resp.Data) > 0 {
					return nil
				}

				return fmt.Errorf("cannot publish outbox message to topic %s with pubsub %s: outbox state not found", c.publishTopic, c.publishPubSub)
			}, bo)
			if err != nil {
				if c.outboxDiscardWhenMissingState {
					outboxLogger.Errorf("failed to publish outbox topic to pubsub %s: %s, discarding message", c.publishPubSub, err)
					//lint:ignore nilerr dropping message
					return nil
				}

				outboxLogger.Errorf("failed to publish outbox topic to pubsub %s: %s, rejecting for later processing", c.publishPubSub, err)
				return err
			}

			cloudEvent[contribPubsub.TopicField] = c.publishTopic
			cloudEvent[contribPubsub.PubsubField] = c.publishPubSub

			b, err := json.Marshal(cloudEvent)
			if err != nil {
				return err
			}

			contentType := cloudEvent[contribPubsub.DataContentTypeField].(string)

			err = o.publisher.Publish(ctx, &contribPubsub.PublishRequest{
				PubsubName:  c.publishPubSub,
				Data:        b,
				Topic:       c.publishTopic,
				ContentType: &contentType,
			})
			if err != nil {
				return err
			}

			err = backoff.Retry(func() error {
				err = store.Delete(ctx, &state.DeleteRequest{
					Key: stateKey,
				})
				if err != nil {
					return err
				}

				return nil
			}, bo)
			return err
		})
	}

	return nil
}
