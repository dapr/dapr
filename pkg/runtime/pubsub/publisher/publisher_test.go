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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
)

func TestPublish(t *testing.T) {
	t.Run("test bulk publish, topic allowed", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{Component: &mockPublishPubSub{}})
		ps := New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  resiliency.New(logger.NewLogger("test")),
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{Component: &mockPublishPubSub{}})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})

		ps := New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  resiliency.New(logger.NewLogger("test")),
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic not allowed", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})

		ps := New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  resiliency.New(logger.NewLogger("test")),
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("test bulk publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})

		ps := New(Options{
			Resiliency:  resiliency.New(logger.NewLogger("test")),
			GetPubSubFn: compStore.GetPubSub,
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component: &mockPublishPubSub{},
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"

		ps := New(Options{
			Resiliency:  resiliency.New(logger.NewLogger("test")),
			GetPubSubFn: compStore.GetPubSub,
		})

		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		require.NoError(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})
		require.NoError(t, err)
	})

	t.Run("test publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})
		ps := New(Options{
			Resiliency:  resiliency.New(logger.NewLogger("test")),
			GetPubSubFn: compStore.GetPubSub,
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})
		require.NoError(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})
		require.NoError(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		ps := New(Options{
			Resiliency:  resiliency.New(logger.NewLogger("test")),
			GetPubSubFn: compStore.GetPubSub,
		})

		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		require.Error(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
		})
		require.Error(t, err)
	})

	t.Run("test publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		ps := New(Options{
			Resiliency:  resiliency.New(logger.NewLogger("test")),
			GetPubSubFn: compStore.GetPubSub,
		})

		compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
		})
		require.Error(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})
		require.Error(t, err)
	})
}

func TestNamespacedPublisher(t *testing.T) {
	compStore := compstore.New()
	compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
		Component:       &mockPublishPubSub{},
		NamespaceScoped: true,
	})

	ps := New(Options{
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		GetPubSubFn: compStore.GetPubSub,
		Namespace:   "ns1",
	})

	err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
		PubsubName: TestPubsubName,
		Topic:      "topic0",
	})
	require.NoError(t, err)

	pubSub, ok := compStore.GetPubSub(TestPubsubName)
	require.True(t, ok)
	assert.Equal(t, "ns1topic0", pubSub.Component.(*mockPublishPubSub).PublishedRequest.Load().Topic)
}

type mockPublishPubSub struct {
	PublishedRequest atomic.Pointer[contribpubsub.PublishRequest]
}

// Init is a mock initialization method.
func (m *mockPublishPubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method.
func (m *mockPublishPubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	m.PublishedRequest.Store(req)
	return nil
}

// BulkPublish is a mock bulk publish method returning a success all the time.
func (m *mockPublishPubSub) BulkPublish(req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	return contribpubsub.BulkPublishResponse{}, nil
}

func (m *mockPublishPubSub) BulkSubscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.BulkHandler) (contribpubsub.BulkSubscribeResponse, error) {
	return contribpubsub.BulkSubscribeResponse{}, nil
}

// Subscribe is a mock subscribe method.
func (m *mockPublishPubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	return nil
}

func (m *mockPublishPubSub) Close() error {
	return nil
}

func (m *mockPublishPubSub) Features() []contribpubsub.Feature {
	return nil
}

func TestPubsubWithResiliency(t *testing.T) {
	t.Run("pubsub publish retries with resiliency", func(t *testing.T) {
		failingPubsub := daprt.FailingPubsub{
			Failure: daprt.NewFailure(
				map[string]int{
					"failingTopic": 1,
				},
				map[string]time.Duration{
					"timeoutTopic": time.Second * 10,
				},
				map[string]int{},
			),
		}

		compStore := compstore.New()
		compStore.AddPubSub("failPubsub", &rtpubsub.PubsubItem{Component: &failingPubsub})

		ps := New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  resiliency.FromConfigurations(logger.NewLogger("test"), daprt.TestResiliency),
		})

		req := &contribpubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "failingTopic",
		}
		err := ps.Publish(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("failingTopic"))
	})

	t.Run("pubsub publish times out with resiliency", func(t *testing.T) {
		failingPubsub := daprt.FailingPubsub{
			Failure: daprt.NewFailure(
				map[string]int{
					"failingTopic": 1,
				},
				map[string]time.Duration{
					"timeoutTopic": time.Second * 10,
				},
				map[string]int{},
			),
		}

		compStore := compstore.New()
		compStore.AddPubSub("failPubsub", &rtpubsub.PubsubItem{Component: &failingPubsub})

		ps := New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  resiliency.FromConfigurations(logger.NewLogger("test"), daprt.TestResiliency),
		})

		req := &contribpubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "timeoutTopic",
		}

		start := time.Now()
		err := ps.Publish(context.Background(), req)
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("timeoutTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}
