package testing

import (
	"context"
	"sync"

	mock "github.com/stretchr/testify/mock"

	"github.com/dapr/components-contrib/pubsub"
)

// MockPubSub is a mock pub-sub component object.
type MockPubSub struct {
	mock.Mock
}

// Init is a mock initialization method.
func (m *MockPubSub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}

// Publish is a mock publish method.
func (m *MockPubSub) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	args := m.Called(req)
	return args.Error(0)
}

// BulkPublish is a mock bulk publish method.
func (m *MockPubSub) BulkPublish(ctx context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	args := m.Called(req)
	return pubsub.BulkPublishResponse{}, args.Error(0)
}

// BulkSubscribe is a mock bulk subscribe method.
func (m *MockPubSub) BulkSubscribe(rctx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) (pubsub.BulkSubscribeResponse, error) {
	args := m.Called(req)
	return pubsub.BulkSubscribeResponse{}, args.Error(0)
}

// Subscribe is a mock subscribe method.
func (m *MockPubSub) Subscribe(_ context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	args := m.Called(req, handler)
	return args.Error(0)
}

// Close is a mock close method.
func (m *MockPubSub) Close() error {
	return nil
}

func (m *MockPubSub) Features() []pubsub.Feature {
	args := m.Called()
	return args.Get(0).([]pubsub.Feature)
}

// FailingPubsub is a mock pubsub component object that simulates failures.
type FailingPubsub struct {
	Failure Failure
}

func (f *FailingPubsub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	return nil
}

func (f *FailingPubsub) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	return f.Failure.PerformFailure(req.Topic)
}

func (f *FailingPubsub) BulkPublish(ctx context.Context, req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	return pubsub.BulkPublishResponse{}, f.Failure.PerformFailure(req.Topic)
}

func (f *FailingPubsub) BulkSubscribe(ctx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) (pubsub.BulkSubscribeResponse, error) {
	return pubsub.BulkSubscribeResponse{}, f.Failure.PerformFailure(req.Topic)
}

func (f *FailingPubsub) Subscribe(_ context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	err := f.Failure.PerformFailure(req.Topic)
	if err != nil {
		return err
	}

	// This handler can also be calling into things that'll fail
	return handler(context.Background(), &pubsub.NewMessage{
		Topic: req.Topic,
		Metadata: map[string]string{
			"pubsubName": "failPubsub",
		},
		Data: []byte(req.Topic),
	})
}

func (f *FailingPubsub) Close() error {
	return nil
}

func (f *FailingPubsub) Features() []pubsub.Feature {
	return nil
}

// InMemoryPubsub is a mock pub-sub component object that works with in-memory handlers
type InMemoryPubsub struct {
	mock.Mock

	subscribedTopics map[string]subscription
	topicsCb         func([]string)
	bulkHandler      func(topic string, msg *pubsub.BulkMessage)
	handler          func(topic string, msg *pubsub.NewMessage)
	lock             *sync.Mutex
}

type subscription struct {
	cancel    context.CancelFunc
	send      chan *pubsub.NewMessage
	sendBatch chan *pubsub.BulkMessage
}

// Init is a mock initialization method.
func (m *InMemoryPubsub) Init(ctx context.Context, metadata pubsub.Metadata) error {
	m.lock = &sync.Mutex{}
	args := m.Called(metadata)
	return args.Error(0)
}

// Publish is a mock publish method.
func (m *InMemoryPubsub) Publish(ctx context.Context, req *pubsub.PublishRequest) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	var send chan *pubsub.NewMessage
	t, ok := m.subscribedTopics[req.Topic]
	if ok && t.send != nil {
		send = t.send
	}
	if send != nil {
		send <- &pubsub.NewMessage{
			Data:        req.Data,
			Topic:       req.Topic,
			Metadata:    req.Metadata,
			ContentType: req.ContentType,
		}
	}
	return nil
}

func (m *InMemoryPubsub) BulkPublish(req *pubsub.BulkPublishRequest) (pubsub.BulkPublishResponse, error) {
	var sendBatch chan *pubsub.BulkMessage
	bulkMessage := pubsub.BulkMessage{}
	if req != nil {
		m.lock.Lock()
		t, ok := m.subscribedTopics[req.Topic]
		if ok && t.sendBatch != nil {
			sendBatch = t.sendBatch
		}
		m.lock.Unlock()
		bulkMessage.Entries = make([]pubsub.BulkMessageEntry, len(req.Entries))
		for i, datum := range req.Entries {
			bulkMessage.Entries[i] = datum
		}
	}
	bulkMessage.Metadata = req.Metadata
	bulkMessage.Topic = req.Topic

	if sendBatch != nil {
		sendBatch <- &bulkMessage
	}

	return pubsub.BulkPublishResponse{}, nil
}

func (m *InMemoryPubsub) BulkSubscribe(parentCtx context.Context, req pubsub.SubscribeRequest, handler pubsub.BulkHandler) (pubsub.BulkSubscribeResponse, error) {
	ctx, cancel := context.WithCancel(parentCtx)

	ch := make(chan *pubsub.BulkMessage, 10)
	m.lock.Lock()
	if m.subscribedTopics == nil {
		m.subscribedTopics = map[string]subscription{}
	}
	m.subscribedTopics[req.Topic] = subscription{
		cancel:    cancel,
		sendBatch: ch,
	}
	m.onSubscribedTopicsChanged()
	m.lock.Unlock()

	go func() {
		for {
			select {
			case msg := <-ch:
				if m.bulkHandler != nil && msg != nil {
					go m.bulkHandler(req.Topic, msg)
				}
			case <-ctx.Done():
				close(ch)
				m.lock.Lock()
				delete(m.subscribedTopics, req.Topic)
				m.onSubscribedTopicsChanged()
				m.lock.Unlock()
				m.MethodCalled("unsubscribed", req.Topic)
				return
			}
		}
	}()

	args := m.Called(req, handler)
	return pubsub.BulkSubscribeResponse{}, args.Error(0)
}

// Subscribe is a mock subscribe method.
func (m *InMemoryPubsub) Subscribe(parentCtx context.Context, req pubsub.SubscribeRequest, handler pubsub.Handler) error {
	ctx, cancel := context.WithCancel(parentCtx)

	ch := make(chan *pubsub.NewMessage, 10)
	m.lock.Lock()
	if m.subscribedTopics == nil {
		m.subscribedTopics = map[string]subscription{}
	}
	m.subscribedTopics[req.Topic] = subscription{
		cancel: cancel,
		send:   ch,
	}
	m.onSubscribedTopicsChanged()
	m.lock.Unlock()

	go func() {
		for {
			select {
			case msg := <-ch:
				if m.handler != nil && msg != nil {
					go m.handler(req.Topic, msg)
				}
			case <-ctx.Done():
				m.lock.Lock()
				close(ch)
				delete(m.subscribedTopics, req.Topic)
				m.onSubscribedTopicsChanged()
				m.MethodCalled("unsubscribed", req.Topic)
				m.lock.Unlock()
				return
			}
		}
	}()

	args := m.Called(req, handler)
	return args.Error(0)
}

// Close is a mock close method.
func (m *InMemoryPubsub) Close() error {
	m.lock.Lock()
	if len(m.subscribedTopics) > 0 {
		for _, f := range m.subscribedTopics {
			f.cancel()
		}
	}
	m.lock.Unlock()
	return nil
}

func (m *InMemoryPubsub) Features() []pubsub.Feature {
	return nil
}

func (m *InMemoryPubsub) SetHandler(h func(topic string, msg *pubsub.NewMessage)) {
	m.handler = h
}

func (m *InMemoryPubsub) SetBulkHandler(h func(topic string, msg *pubsub.BulkMessage)) {
	m.bulkHandler = h
}

func (m *InMemoryPubsub) SetOnSubscribedTopicsChanged(f func([]string)) {
	m.topicsCb = f
}

func (m *InMemoryPubsub) onSubscribedTopicsChanged() {
	if m.topicsCb != nil {
		topics := make([]string, len(m.subscribedTopics))
		i := 0
		for k := range m.subscribedTopics {
			topics[i] = k
			i++
		}
		m.topicsCb(topics)
	}
}
