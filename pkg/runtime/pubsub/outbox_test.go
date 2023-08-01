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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/outbox"
)

func newTestOutbox() outbox.Outbox {
	o := NewOutbox(func(ctx context.Context, req *contribPubsub.PublishRequest) error { return nil }, func(s string) (contribPubsub.PubSub, bool) { return nil, false }, func(s string) (state.Store, bool) { return nil, false }, func(m map[string]any, s string) string { return "" })
	return o
}

func TestNewOutbox(t *testing.T) {
	o := newTestOutbox()
	assert.NotNil(t, o)
}

func TestEnabled(t *testing.T) {
	t.Run("required config", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		assert.True(t, o.Enabled("test"))
		assert.False(t, o.Enabled("test1"))
	})

	t.Run("missing pubsub config", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		assert.False(t, o.Enabled("test"))
		assert.False(t, o.Enabled("test1"))
	})

	t.Run("missing topic config", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
				},
			},
		})

		assert.False(t, o.Enabled("test"))
		assert.False(t, o.Enabled("test1"))
	})
}

func TestAddOrUpdateOutbox(t *testing.T) {
	t.Run("config values correct", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
					{
						Name: outboxStateScanDelayKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("8s"),
							},
						},
					},
					{
						Name: outboxPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("2"),
							},
						},
					},
				},
			},
		})

		c := o.outboxStores["test"]
		assert.Equal(t, c.outboxPubsub, "2")
		assert.Equal(t, c.stateScanDelay, "8s")
		assert.Equal(t, c.publishPubSub, "a")
		assert.Equal(t, c.publishTopic, "1")
	})

	t.Run("config default values correct", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		c := o.outboxStores["test"]
		assert.Equal(t, c.outboxPubsub, "a")
		assert.Equal(t, c.stateScanDelay, defaultStateScanDelay)
		assert.Equal(t, c.publishPubSub, "a")
		assert.Equal(t, c.publishTopic, "1")
	})
}

func TestPublishInternal(t *testing.T) {
	t.Run("valid operation, correct default parameters", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			var cloudEvent map[string]interface{}
			err := json.Unmarshal(pr.Data, &cloudEvent)
			assert.NoError(t, err)

			assert.Equal(t, "test", cloudEvent["data"])
			assert.Equal(t, "a", pr.PubsubName)
			assert.Equal(t, "testapp1outbox", pr.Topic)
			assert.Equal(t, "testapp", cloudEvent["source"])
			assert.Equal(t, "text/plain", cloudEvent["datacontenttype"])
			assert.Equal(t, "a", cloudEvent["pubsubname"])

			return nil
		}

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "key",
				Value: "test",
			},
		}, "testapp")

		assert.NoError(t, err)
	})

	t.Run("valid operation, custom datacontenttype", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			var cloudEvent map[string]interface{}
			err := json.Unmarshal(pr.Data, &cloudEvent)
			assert.NoError(t, err)

			assert.Equal(t, "test", cloudEvent["data"])
			assert.Equal(t, "a", pr.PubsubName)
			assert.Equal(t, "testapp1outbox", pr.Topic)
			assert.Equal(t, "testapp", cloudEvent["source"])
			assert.Equal(t, "application/json", cloudEvent["datacontenttype"])
			assert.Equal(t, "a", cloudEvent["pubsubname"])

			return nil
		}

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		contentType := "application/json"
		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:         "key",
				Value:       "test",
				ContentType: &contentType,
			},
		}, "testapp")

		assert.NoError(t, err)
	})

	t.Run("missing state store", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "key",
				Value: "test",
			},
		}, "testapp")
		assert.Error(t, err)
	})

	t.Run("no op when no transactions", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			assert.Fail(t, "unexptected message received")
			return nil
		}

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{}, "testapp")

		assert.NoError(t, err)
	})

	t.Run("error when pubsub fails", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			return errors.New("")
		}

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
				},
			},
		})

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "1",
				Value: "hello",
			},
		}, "testapp")

		assert.Error(t, err)
	})
}

func TestSubscribeToInternalTopics(t *testing.T) {
	t.Run("correct configuration", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.cloudEventExtractorFn = extractCloudEventProperty

		outboxTopic := "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}
		stateMock := &outboxStateMock{}

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				psMock.internalCalled = true
			} else if pr.Topic == "1" {
				psMock.externalCalled = true
			}

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return stateMock, true
		}

		stateScan := "100ms"

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
					{
						Name: outboxStateScanDelayKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte(stateScan),
							},
						},
					},
				},
			},
		})

		appID := "test"
		err := o.SubscribeToInternalTopics(context.TODO(), appID)
		assert.NoError(t, err)

		go func() {
			trs, pErr := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
				state.SetRequest{
					Key:   "1",
					Value: "hello",
				},
			}, appID)

			assert.NoError(t, pErr)
			assert.Len(t, trs, 1)

			stateMock.expectedKey = trs[0].GetKey()
		}()

		d, err := time.ParseDuration(stateScan)
		assert.NoError(t, err)

		time.Sleep(d * 2)
		assert.True(t, psMock.internalCalled)
		assert.True(t, psMock.externalCalled)
		assert.Equal(t, stateMock.expectedKey, stateMock.receivedKey)
	})

	t.Run("state store not present", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		outboxTopic := "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				psMock.internalCalled = true
			} else if pr.Topic == "1" {
				psMock.externalCalled = true
			}

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return nil, false
		}

		appID := "test"
		err := o.SubscribeToInternalTopics(context.TODO(), appID)
		assert.NoError(t, err)

		trs, pErr := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "1",
				Value: "hello",
			},
		}, appID)

		assert.Error(t, pErr)
		assert.Len(t, trs, 0)
	})

	t.Run("outbox state not present", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		outboxTopic := "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}
		stateMock := &outboxStateMock{}

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				psMock.internalCalled = true
			} else if pr.Topic == "1" {
				psMock.externalCalled = true
			}

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return stateMock, true
		}

		stateScan := "100ms"

		o.AddOrUpdateOutbox(v1alpha1.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: v1alpha1.ComponentSpec{
				Metadata: []common.NameValuePair{
					{
						Name: outboxPublishPubsubKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("a"),
							},
						},
					},
					{
						Name: outboxPublishTopicKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("1"),
							},
						},
					},
					{
						Name: outboxStateScanDelayKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte(stateScan),
							},
						},
					},
				},
			},
		})

		appID := "test"
		err := o.SubscribeToInternalTopics(context.TODO(), appID)
		assert.NoError(t, err)

		go func() {
			trs, pErr := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
				state.SetRequest{
					Key:   "1",
					Value: "hello",
				},
			}, appID)

			assert.NoError(t, pErr)
			assert.Len(t, trs, 1)
		}()

		d, err := time.ParseDuration(stateScan)
		assert.NoError(t, err)

		time.Sleep(d * 2)
		assert.True(t, psMock.internalCalled)
		assert.False(t, psMock.externalCalled)
	})
}

type outboxPubsubMock struct {
	expectedOutboxTopic string
	t                   *testing.T
	handler             contribPubsub.Handler
	internalCalled      bool
	externalCalled      bool
}

func (o *outboxPubsubMock) Init(ctx context.Context, metadata contribPubsub.Metadata) error {
	return nil
}

func (o *outboxPubsubMock) Features() []contribPubsub.Feature {
	return nil
}

func (o *outboxPubsubMock) Publish(ctx context.Context, req *contribPubsub.PublishRequest) error {
	go func() {
		err := o.handler(context.TODO(), &contribPubsub.NewMessage{
			Data:  req.Data,
			Topic: req.Topic,
		})
		assert.NoError(o.t, err)
	}()

	return nil
}

func (o *outboxPubsubMock) Subscribe(ctx context.Context, req contribPubsub.SubscribeRequest, handler contribPubsub.Handler) error {
	if req.Topic != o.expectedOutboxTopic {
		assert.Fail(o.t, fmt.Sprintf("expected outbox topic %s, got %s", o.expectedOutboxTopic, req.Topic))
	}

	o.handler = handler
	return nil
}

func (o *outboxPubsubMock) Close() error {
	return nil
}

type outboxStateMock struct {
	expectedKey string
	receivedKey string
}

func (o *outboxStateMock) Init(ctx context.Context, metadata state.Metadata) error {
	return nil
}

func (o *outboxStateMock) Features() []state.Feature {
	return nil
}

func (o *outboxStateMock) Delete(ctx context.Context, req *state.DeleteRequest) error {
	return nil
}

func (o *outboxStateMock) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	o.receivedKey = req.Key

	if o.expectedKey != "" && o.expectedKey == o.receivedKey {
		return &state.GetResponse{
			Data: []byte("0"),
		}, nil
	}

	return nil, nil
}

func (o *outboxStateMock) Set(ctx context.Context, req *state.SetRequest) error {
	return nil
}

func (o *outboxStateMock) BulkGet(ctx context.Context, req []state.GetRequest, opts state.BulkGetOpts) ([]state.BulkGetResponse, error) {
	return nil, nil
}

func (o *outboxStateMock) BulkSet(ctx context.Context, req []state.SetRequest, opts state.BulkStoreOpts) error {
	return nil
}

func (o *outboxStateMock) BulkDelete(ctx context.Context, req []state.DeleteRequest, opts state.BulkStoreOpts) error {
	return nil
}

func extractCloudEventProperty(cloudEvent map[string]any, property string) string {
	if cloudEvent == nil {
		return ""
	}
	iValue, ok := cloudEvent[property]
	if ok {
		if value, ok := iValue.(string); ok {
			return value
		}
	}

	return ""
}
