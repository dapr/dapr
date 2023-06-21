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

//nolint:goimports
package runtime

import (
	"context"
	"sync/atomic"
	"testing"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"

	fuzz "github.com/AdamKorcz/go-fuzz-headers-1"

	"github.com/dapr/components-contrib/pubsub"
	cryptoLoader "github.com/dapr/dapr/pkg/components/crypto"
	workflowsLoader "github.com/dapr/dapr/pkg/components/workflows"
)

var fuzzRT *DaprRuntime

func init() {
	l := logger.NewLogger("test")
	l.SetOutputLevel(logger.FatalLevel)
	log.SetOutputLevel(logger.FatalLevel)
	fuzzRT = NewTestDaprRuntime(modes.StandaloneMode)
	if fuzzRT == nil {
		panic("fuzzRT is nil so stopping the fuzzer")
	}
}

type mockPublishPubSubFuzz struct {
	PublishedRequest atomic.Pointer[pubsub.PublishRequest]
	data             []byte
}

// Init is a mock initialization method.
func (m *mockPublishPubSubFuzz) Init(ctx context.Context, metadata pubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method.
func (m *mockPublishPubSubFuzz) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	m.PublishedRequest.Store(req)
	return nil
}

// BulkPublish is a mock bulk publish method returning a success all the time.
func (m *mockPublishPubSubFuzz) BulkPublish(req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	return pubsub.BulkPublishResponse{}, nil
}

func (m *mockPublishPubSubFuzz) BulkSubscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) (pubsub.BulkSubscribeResponse, error) {
	return pubsub.BulkSubscribeResponse{}, nil
}

// Subscribe is a mock subscribe method.
func (m *mockPublishPubSubFuzz) Subscribe(_ context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	message := &pubsub.NewMessage{}
	ff := fuzz.NewConsumer(m.data)
	ff.GenerateStruct(message)
	handler(context.Background(), message)
	return nil
}

func (m *mockPublishPubSubFuzz) Close() error {
	return nil
}

func (m *mockPublishPubSubFuzz) Features() []pubsub.Feature {
	return nil
}

func (m *mockPublishPubSubFuzz) GetComponentMetadata() map[string]string {
	md := make(map[string]string)
	ff := fuzz.NewConsumer(m.data)
	ff.FuzzMap(&md)
	return md
}

func FuzzDaprRuntime(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, callType int) {
		fuzzRT.workflowComponentRegistry = workflowsLoader.NewRegistry()
		fuzzRT.cryptoProviderRegistry = cryptoLoader.NewRegistry()
		ff := fuzz.NewConsumer(data)
		switch callType % 4 {
		case 0:
			comp := &componentsV1alpha1.Component{}
			ff.GenerateStruct(comp)
			fuzzRT.processComponentAndDependents(*comp)
		case 1:
			pubRequest := &pubsub.PublishRequest{}
			ff.GenerateStruct(pubRequest)
			if pubRequest.PubsubName == "" {
				return
			}
			messageData, err := ff.GetBytes()
			if err != nil {
				return
			}
			fuzzRT.compStore.AddPubSub(pubsubName,
				compstore.PubsubItem{
					Component: &mockPublishPubSubFuzz{
						data: messageData,
					},
				})
			defer func() {
				fuzzRT.compStore.DeletePubSub(pubsubName)
			}()
			fuzzRT.Publish(pubRequest)
		case 2:
			pubRequest := &pubsub.BulkPublishRequest{}
			ff.GenerateStruct(pubRequest)
			_, _ = fuzzRT.BulkPublish(pubRequest)
		case 3:
			name, err := ff.GetString()
			if err != nil {
				return
			}
			if name == "" {
				return
			}
			routes := make(map[string]compstore.TopicRouteElem)
			ff.FuzzMap(&routes)
			fuzzRT.Subscribe(context.Background(), name, routes)
		}
	})
}
