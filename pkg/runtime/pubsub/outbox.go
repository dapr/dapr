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
	publishFn             func(context.Context, *contribPubsub.PublishRequest) error
	outboxStores          map[string]outboxConfig
	lock                  sync.RWMutex
	namespace             string
}

// NewOutbox returns an instance of an Outbox.
func NewOutbox(publishFn func(context.Context, *contribPubsub.PublishRequest) error, getPubsubFn func(string) (contribPubsub.PubSub, bool), getStateFn func(string) (state.Store, bool), cloudEventExtractorFn func(map[string]any, string) string, namespace string) outbox.Outbox {
	return &outboxImpl{
		cloudEventExtractorFn: cloudEventExtractorFn,
		getPubsubFn:           getPubsubFn,
		getStateFn:            getStateFn,
		publishFn:             publishFn,
		lock:                  sync.RWMutex{},
		outboxStores:          map[string]outboxConfig{},
		namespace:             namespace,
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

// PublishInternal publishes the state to an internal topic for outbox processing
func (o *outboxImpl) PublishInternal(ctx context.Context, stateStore string, operations []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error) {
	o.lock.RLock()
	c, ok := o.outboxStores[stateStore]
	o.lock.RUnlock()

	if !ok {
		return nil, fmt.Errorf("error publishing internal outbox message: could not find outbox configuration on state store %s", stateStore)
	}

	trs := make([]state.TransactionalStateOperation, 0, len(operations))
	for _, op := range operations {
		sr, ok := op.(state.SetRequest)
		if ok {
			tr, err := transaction()
			if err != nil {
				return nil, err
			}

			var ceData []byte
			bt, ok := sr.Value.([]byte)
			if ok {
				ceData = bt
			} else if sr.ContentType != nil && strings.EqualFold(*sr.ContentType, "application/json") {
				b, sErr := json.Marshal(sr.Value)
				if sErr != nil {
					return nil, sErr
				}

				ceData = b
			} else {
				ceData = []byte(fmt.Sprintf("%v", sr.Value))
			}

			ce := &CloudEvent{
				ID:         tr.GetKey(),
				Source:     source,
				Pubsub:     c.outboxPubsub,
				Data:       ceData,
				TraceID:    traceID,
				TraceState: traceState,
			}

			if sr.ContentType != nil {
				ce.DataContentType = *sr.ContentType
			}

			msg, err := NewCloudEvent(ce, nil)
			if err != nil {
				return nil, err
			}

			data, err := json.Marshal(msg)
			if err != nil {
				return nil, err
			}

			err = o.publishFn(ctx, &contribPubsub.PublishRequest{
				PubsubName: c.outboxPubsub,
				Data:       data,
				Topic:      outboxTopic(source, c.publishTopic, o.namespace),
			})
			if err != nil {
				return nil, err
			}

			trs = append(trs, tr)
		}
	}

	return trs, nil
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
			data := []byte(o.cloudEventExtractorFn(cloudEvent, contribPubsub.DataField))
			contentType := o.cloudEventExtractorFn(cloudEvent, contribPubsub.DataContentTypeField)
			traceID := o.cloudEventExtractorFn(cloudEvent, contribPubsub.TraceIDField)
			traceState := o.cloudEventExtractorFn(cloudEvent, contribPubsub.TraceStateField)

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

			ce, err := NewCloudEvent(&CloudEvent{
				Data:            data,
				DataContentType: contentType,
				Pubsub:          c.publishPubSub,
				Source:          appID,
				Topic:           c.publishTopic,
				TraceID:         traceID,
				TraceState:      traceState,
			}, nil)
			if err != nil {
				return err
			}

			b, err := json.Marshal(ce)
			if err != nil {
				return err
			}

			err = o.publishFn(ctx, &contribPubsub.PublishRequest{
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
