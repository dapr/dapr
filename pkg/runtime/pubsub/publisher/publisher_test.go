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
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
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
		res, err := ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err = ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err := ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err = ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err := ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err = ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err := ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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
		res, err = ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
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

		err := ps.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		require.NoError(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		err = ps.Publish(t.Context(), &contribpubsub.PublishRequest{
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
		err := ps.Publish(t.Context(), &contribpubsub.PublishRequest{
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
		err = ps.Publish(t.Context(), &contribpubsub.PublishRequest{
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
		err := ps.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		require.Error(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err = ps.Publish(t.Context(), &contribpubsub.PublishRequest{
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
		err := ps.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
		})
		require.Error(t, err)

		compStore.AddPubSub(TestSecondPubsubName, &rtpubsub.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err = ps.Publish(t.Context(), &contribpubsub.PublishRequest{
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

	err := ps.Publish(t.Context(), &contribpubsub.PublishRequest{
		PubsubName: TestPubsubName,
		Topic:      "topic0",
	})
	require.NoError(t, err)

	pubSub, ok := compStore.GetPubSub(TestPubsubName)
	require.True(t, ok)
	assert.Equal(t, "ns1topic0", pubSub.Component.(*mockPublishPubSub).PublishedRequest.Load().Topic)
}

func TestNamespacedBulkPublisher(t *testing.T) {
	compStore := compstore.New()
	compStore.AddPubSub(TestPubsubName, &rtpubsub.PubsubItem{
		Component:       &mockBulkPublishPubSub{},
		NamespaceScoped: true,
	})

	ps := New(Options{
		Resiliency:  resiliency.New(logger.NewLogger("test")),
		GetPubSubFn: compStore.GetPubSub,
		Namespace:   "ns1",
	})

	res, err := ps.BulkPublish(t.Context(), &contribpubsub.BulkPublishRequest{
		PubsubName: TestPubsubName,
		Topic:      "topic0",
		Entries: []contribpubsub.BulkMessageEntry{
			{
				EntryId:     "1",
				Event:       []byte("test"),
				ContentType: "text/plain",
			},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, res.FailedEntries)

	pubSub, ok := compStore.GetPubSub(TestPubsubName)
	require.True(t, ok)
	bulkReq := pubSub.Component.(*mockBulkPublishPubSub).BulkPublishedRequest.Load()
	require.NotNil(t, bulkReq)
	assert.Equal(t, "ns1topic0", bulkReq.Topic)
}

type mockBulkPublishPubSub struct {
	BulkPublishedRequest atomic.Pointer[contribpubsub.BulkPublishRequest]
}

func (m *mockBulkPublishPubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	return nil
}

func (m *mockBulkPublishPubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	return nil
}

func (m *mockBulkPublishPubSub) BulkPublish(ctx context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	m.BulkPublishedRequest.Store(req)
	return contribpubsub.BulkPublishResponse{}, nil
}

func (m *mockBulkPublishPubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	return nil
}

func (m *mockBulkPublishPubSub) BulkSubscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.BulkHandler) (contribpubsub.BulkSubscribeResponse, error) {
	return contribpubsub.BulkSubscribeResponse{}, nil
}

func (m *mockBulkPublishPubSub) Close() error {
	return nil
}

func (m *mockBulkPublishPubSub) Features() []contribpubsub.Feature {
	return []contribpubsub.Feature{contribpubsub.FeatureBulkPublish}
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

// matchingPubSub is a pubsub mock whose Publish returns a configurable error per
// call, used to verify that retry `matching` is honored on the publish path.
type matchingPubSub struct {
	calls     atomic.Int32
	publishFn func(call int32) error
}

func (m *matchingPubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	return nil
}

func (m *matchingPubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	return m.publishFn(m.calls.Add(1))
}

func (m *matchingPubSub) BulkPublish(ctx context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	return contribpubsub.BulkPublishResponse{}, m.publishFn(m.calls.Add(1))
}

func (m *matchingPubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	return nil
}

func (m *matchingPubSub) BulkSubscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.BulkHandler) (contribpubsub.BulkSubscribeResponse, error) {
	return contribpubsub.BulkSubscribeResponse{}, nil
}

func (m *matchingPubSub) Close() error {
	return nil
}

func (m *matchingPubSub) Features() []contribpubsub.Feature {
	return nil
}

// matchingResiliency builds a provider whose pubsub outbound policy retries up to
// maxRetries with the given `matching` rules applied to component "failPubsub".
func matchingResiliency(maxRetries int, matching *resiliencyV1alpha.RetryMatching) resiliency.Provider {
	cfg := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
					"pubsubRetry": {
						MaxRetries: ptr.Of(maxRetries),
						Policy:     "constant",
						Duration:   "1ms",
						Matching:   matching,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"failPubsub": {
						Outbound: resiliencyV1alpha.PolicyNames{Retry: "pubsubRetry"},
					},
				},
			},
		},
	}
	return resiliency.FromConfigurations(logger.NewLogger("test"), cfg)
}

// TestPubsubPublishMatching verifies that, on the publish path, a component error
// carrying a gRPC status is wrapped in a resiliency.CodeError so the configured retry
// `matching` is consulted — and that errors without a status keep retrying as before.
func TestPubsubPublishMatching(t *testing.T) {
	// Only gRPC code 14 (Unavailable) is configured as retriable.
	matching := &resiliencyV1alpha.RetryMatching{GRPCStatusCodes: "14"}

	newPublisher := func(ps contribpubsub.PubSub, maxRetries int) rtpubsub.Adapter {
		compStore := compstore.New()
		compStore.AddPubSub("failPubsub", &rtpubsub.PubsubItem{Component: ps})
		return New(Options{
			GetPubSubFn: compStore.GetPubSub,
			Resiliency:  matchingResiliency(maxRetries, matching),
		})
	}

	req := &contribpubsub.PublishRequest{PubsubName: "failPubsub", Topic: "topic"}

	t.Run("non-retriable gRPC status code is not retried", func(t *testing.T) {
		ps := &matchingPubSub{publishFn: func(int32) error {
			return status.Error(codes.InvalidArgument, "bad request")
		}}

		err := newPublisher(ps, 5).Publish(t.Context(), req)

		require.Error(t, err)
		// Code 3 is not in the retriable list, so matching breaks the retry loop after 1 try
		assert.Equal(t, int32(1), ps.calls.Load())
	})

	t.Run("retriable gRPC status code is retried until success", func(t *testing.T) {
		ps := &matchingPubSub{publishFn: func(call int32) error {
			if call <= 2 {
				return status.Error(codes.Unavailable, "try later")
			}
			return nil
		}}

		err := newPublisher(ps, 5).Publish(t.Context(), req)

		require.NoError(t, err)
		// 2 failures then success = 3 calls
		assert.Equal(t, int32(3), ps.calls.Load())
	})

	t.Run("plain error without status still retries (no regression)", func(t *testing.T) {
		ps := &matchingPubSub{publishFn: func(int32) error {
			return errors.New("boom")
		}}

		err := newPublisher(ps, 3).Publish(t.Context(), req)

		require.Error(t, err)
		// No gRPC status = retried up to maxRetries (initial + 3)
		assert.Equal(t, int32(4), ps.calls.Load())
	})

	t.Run("terminal code (FailedPrecondition) is not retried", func(t *testing.T) {
		ps := &matchingPubSub{publishFn: func(int32) error {
			return status.Error(codes.FailedPrecondition, "invalid topic")
		}}

		err := newPublisher(ps, 5).Publish(t.Context(), req)

		require.Error(t, err)
		assert.Equal(t, int32(1), ps.calls.Load())
	})

	t.Run("retriable code (Unavailable) is retried until success", func(t *testing.T) {
		ps := &matchingPubSub{publishFn: func(call int32) error {
			if call <= 2 {
				return status.Error(codes.Unavailable, "broker unavailable")
			}
			return nil
		}}

		err := newPublisher(ps, 5).Publish(t.Context(), req)

		require.NoError(t, err)
		assert.Equal(t, int32(3), ps.calls.Load())
	})
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
		err := ps.Publish(t.Context(), req)

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
		err := ps.Publish(t.Context(), req)
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("timeoutTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}
