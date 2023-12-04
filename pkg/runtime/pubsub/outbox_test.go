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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribPubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/outbox"
	"github.com/dapr/kit/ptr"
)

func newTestOutbox() outbox.Outbox {
	o := NewOutbox(func(ctx context.Context, req *contribPubsub.PublishRequest) error { return nil }, func(s string) (contribPubsub.PubSub, bool) { return nil, false }, func(s string) (state.Store, bool) { return nil, false }, func(m map[string]any, s string) string { return "" }, "")
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
		assert.Equal(t, "2", c.outboxPubsub)
		assert.Equal(t, "a", c.publishPubSub)
		assert.Equal(t, "1", c.publishTopic)
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
		assert.Equal(t, "a", c.outboxPubsub)
		assert.Equal(t, "a", c.publishPubSub)
		assert.Equal(t, "1", c.publishTopic)
	})
}

func TestPublishInternal(t *testing.T) {
	t.Run("valid operation, correct default parameters", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			var cloudEvent map[string]interface{}
			err := json.Unmarshal(pr.Data, &cloudEvent)
			require.NoError(t, err)

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

		_, err := o.PublishInternal(context.Background(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "key",
				Value: "test",
			},
		}, "testapp", "", "")

		require.NoError(t, err)
	})

	t.Run("valid operation, no datacontenttype", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			var cloudEvent map[string]interface{}
			err := json.Unmarshal(pr.Data, &cloudEvent)
			require.NoError(t, err)

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

		contentType := ""
		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:         "key",
				Value:       "test",
				ContentType: &contentType,
			},
		}, "testapp", "", "")

		require.NoError(t, err)
	})

	type customData struct {
		Name string `json:"name"`
	}

	t.Run("valid operation, application/json datacontenttype", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			var cloudEvent map[string]interface{}
			err := json.Unmarshal(pr.Data, &cloudEvent)
			require.NoError(t, err)

			data := cloudEvent["data"]
			j := customData{}

			err = json.Unmarshal([]byte(data.(string)), &j)
			require.NoError(t, err)

			assert.Equal(t, "test", j.Name)
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

		j := customData{
			Name: "test",
		}
		b, err := json.Marshal(&j)
		require.NoError(t, err)

		contentType := "application/json"
		_, err = o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:         "key",
				Value:       string(b),
				ContentType: &contentType,
			},
		}, "testapp", "", "")

		require.NoError(t, err)
	})

	t.Run("missing state store", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "key",
				Value: "test",
			},
		}, "testapp", "", "")
		require.Error(t, err)
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

		_, err := o.PublishInternal(context.TODO(), "test", []state.TransactionalStateOperation{}, "testapp", "", "")

		require.NoError(t, err)
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
		}, "testapp", "", "")

		require.Error(t, err)
	})
}

func TestSubscribeToInternalTopics(t *testing.T) {
	t.Run("correct configuration with trace", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.cloudEventExtractorFn = extractCloudEventProperty

		const outboxTopic = "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}
		stateMock := &outboxStateMock{
			receivedKey: make(chan string, 1),
		}

		internalCalledCh := make(chan struct{})
		externalCalledCh := make(chan struct{})

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				close(internalCalledCh)
			} else if pr.Topic == "1" {
				close(externalCalledCh)
			}

			ce := map[string]string{}
			json.Unmarshal(pr.Data, &ce)

			traceID := ce[contribPubsub.TraceIDField]
			traceState := ce[contribPubsub.TraceStateField]
			assert.Equal(t, "00-ecdf5aaa79bff09b62b201442c0f3061-d2597ed7bfd029e4-01", traceID)
			assert.Equal(t, "00-ecdf5aaa79bff09b62b201442c0f3061-d2597ed7bfd029e4-01", traceState)

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return stateMock, true
		}

		stateScan := "1s"

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

		const appID = "test"
		err := o.SubscribeToInternalTopics(context.Background(), appID)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			trs, pErr := o.PublishInternal(context.Background(), "test", []state.TransactionalStateOperation{
				state.SetRequest{
					Key:   "1",
					Value: "hello",
				},
			}, appID, "00-ecdf5aaa79bff09b62b201442c0f3061-d2597ed7bfd029e4-01", "00-ecdf5aaa79bff09b62b201442c0f3061-d2597ed7bfd029e4-01")

			if pErr != nil {
				errCh <- pErr
				return
			}
			if len(trs) != 1 {
				errCh <- fmt.Errorf("expected trs to have len(1), but got: %d", len(trs))
				return
			}

			errCh <- nil
			stateMock.expectedKey.Store(ptr.Of(trs[0].GetKey()))
		}()

		d, err := time.ParseDuration(stateScan)
		require.NoError(t, err)

		start := time.Now()
		doneCh := make(chan error, 2)
		timeout := time.After(5 * time.Second)
		go func() {
			select {
			case <-internalCalledCh:
				doneCh <- nil
			case <-timeout:
				doneCh <- errors.New("timeout waiting for internalCalledCh")
			}
		}()
		go func() {
			select {
			case <-externalCalledCh:
				doneCh <- nil
			case <-timeout:
				doneCh <- errors.New("timeout waiting for externalCalledCh")
			}
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-doneCh)
		}
		require.GreaterOrEqual(t, time.Since(start), d)

		// Publishing should not have errored
		require.NoError(t, <-errCh)

		expected := stateMock.expectedKey.Load()
		require.NotNil(t, expected)
		assert.Equal(t, *expected, <-stateMock.receivedKey)
	})

	t.Run("state store not present", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		const outboxTopic = "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return nil, false
		}

		const appID = "test"
		err := o.SubscribeToInternalTopics(context.Background(), appID)
		require.NoError(t, err)

		trs, pErr := o.PublishInternal(context.Background(), "test", []state.TransactionalStateOperation{
			state.SetRequest{
				Key:   "1",
				Value: "hello",
			},
		}, appID, "", "")

		require.Error(t, pErr)
		assert.Empty(t, trs)
	})

	t.Run("outbox state not present", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		const outboxTopic = "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
		}
		stateMock := &outboxStateMock{}

		internalCalledCh := make(chan struct{})
		externalCalledCh := make(chan struct{})

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				close(internalCalledCh)
			} else if pr.Topic == "1" {
				close(externalCalledCh)
			}

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return stateMock, true
		}

		const stateScan = "1s"

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

		const appID = "test"
		err := o.SubscribeToInternalTopics(context.Background(), appID)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			trs, pErr := o.PublishInternal(context.Background(), "test", []state.TransactionalStateOperation{
				state.SetRequest{
					Key:   "1",
					Value: "hello",
				},
			}, appID, "", "")

			if pErr != nil {
				errCh <- pErr
				return
			}
			if len(trs) != 1 {
				errCh <- fmt.Errorf("expected trs to have len(1), but got: %d", len(trs))
				return
			}
			errCh <- nil
		}()

		d, err := time.ParseDuration(stateScan)
		require.NoError(t, err)

		start := time.Now()
		doneCh := make(chan error, 2)
		timeout := time.After(2 * time.Second)
		go func() {
			select {
			case <-internalCalledCh:
				doneCh <- nil
			case <-timeout:
				doneCh <- errors.New("timeout waiting for internalCalledCh")
			}
		}()
		go func() {
			// Here we expect no signal
			select {
			case <-externalCalledCh:
				doneCh <- errors.New("received unexpected signal on externalCalledCh")
			case <-timeout:
				doneCh <- nil
			}
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-doneCh)
		}
		require.GreaterOrEqual(t, time.Since(start), d)

		// Publishing should not have errored
		require.NoError(t, <-errCh)
	})

	t.Run("outbox state not present with discard", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)

		const outboxTopic = "test1outbox"

		psMock := &outboxPubsubMock{
			expectedOutboxTopic: outboxTopic,
			t:                   t,
			validateNoError:     true,
		}
		stateMock := &outboxStateMock{
			returnEmptyOnGet: true,
		}

		internalCalledCh := make(chan struct{})
		externalCalledCh := make(chan struct{})

		o.publishFn = func(ctx context.Context, pr *contribPubsub.PublishRequest) error {
			if pr.Topic == outboxTopic {
				close(internalCalledCh)
			} else if pr.Topic == "1" {
				close(externalCalledCh)
			}

			return psMock.Publish(ctx, pr)
		}
		o.getPubsubFn = func(s string) (contribPubsub.PubSub, bool) {
			return psMock, true
		}
		o.getStateFn = func(s string) (state.Store, bool) {
			return stateMock, true
		}

		const stateScan = "1s"

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
						Name: outboxDiscardWhenMissingStateKey,
						Value: common.DynamicValue{
							JSON: v1.JSON{
								Raw: []byte("true"),
							},
						},
					},
				},
			},
		})

		const appID = "test"
		err := o.SubscribeToInternalTopics(context.Background(), appID)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			trs, pErr := o.PublishInternal(context.Background(), "test", []state.TransactionalStateOperation{
				state.SetRequest{
					Key:   "1",
					Value: "hello",
				},
			}, appID, "", "")

			if pErr != nil {
				errCh <- pErr
				return
			}
			if len(trs) != 1 {
				errCh <- fmt.Errorf("expected trs to have len(1), but got: %d", len(trs))
				return
			}
			errCh <- nil
		}()

		d, err := time.ParseDuration(stateScan)
		require.NoError(t, err)

		start := time.Now()
		doneCh := make(chan error, 2)

		// account for max retry time
		timeout := time.After(11 * time.Second)
		go func() {
			select {
			case <-internalCalledCh:
				doneCh <- nil
			case <-timeout:
				doneCh <- errors.New("timeout waiting for internalCalledCh")
			}
		}()
		go func() {
			// Here we expect no signal
			select {
			case <-externalCalledCh:
				doneCh <- errors.New("received unexpected signal on externalCalledCh")
			case <-timeout:
				doneCh <- nil
			}
		}()
		for i := 0; i < 2; i++ {
			require.NoError(t, <-doneCh)
		}
		require.GreaterOrEqual(t, time.Since(start), d)

		// Publishing should not have errored
		require.NoError(t, <-errCh)
	})
}

type outboxPubsubMock struct {
	expectedOutboxTopic string
	t                   *testing.T
	handler             contribPubsub.Handler
	validateNoError     bool
}

func (o *outboxPubsubMock) Init(ctx context.Context, metadata contribPubsub.Metadata) error {
	return nil
}

func (o *outboxPubsubMock) Features() []contribPubsub.Feature {
	return nil
}

func (o *outboxPubsubMock) Publish(ctx context.Context, req *contribPubsub.PublishRequest) error {
	go func() {
		err := o.handler(context.Background(), &contribPubsub.NewMessage{
			Data:  req.Data,
			Topic: req.Topic,
		})

		if o.validateNoError {
			require.NoError(o.t, err)
			return
		}
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
	expectedKey      atomic.Pointer[string]
	receivedKey      chan string
	returnEmptyOnGet bool
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
	if o.returnEmptyOnGet {
		return &state.GetResponse{}, nil
	}

	if o.receivedKey != nil {
		o.receivedKey <- req.Key
	}

	expected := o.expectedKey.Load()

	if expected != nil && *expected != "" && *expected == req.Key {
		return &state.GetResponse{
			Data: []byte("0"),
		}, nil
	}

	return nil, nil
}

func TestOutboxTopic(t *testing.T) {
	t.Run("not namespaced", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		topic := outboxTopic("a", "b", o.namespace)

		assert.Equal(t, "aboutbox", topic)
	})

	t.Run("namespaced", func(t *testing.T) {
		o := newTestOutbox().(*outboxImpl)
		o.namespace = "default"

		topic := outboxTopic("a", "b", o.namespace)

		assert.Equal(t, "defaultaboutbox", topic)
	})
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
