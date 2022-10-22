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
func (m *MockPubSub) Init(metadata pubsub.Metadata) error {
	args := m.Called(metadata)
	return args.Error(0)
}

// Publish is a mock publish method.
func (m *MockPubSub) Publish(req *pubsub.PublishRequest) error {
	args := m.Called(req)
	return args.Error(0)
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

func (f *FailingPubsub) Init(metadata pubsub.Metadata) error {
	return nil
}

func (f *FailingPubsub) Publish(req *pubsub.PublishRequest) error {
	return f.Failure.PerformFailure(req.Topic)
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
	handler          func(topic string, msg *pubsub.NewMessage)
	lock             *sync.Mutex
}

type subscription struct {
	cancel context.CancelFunc
	send   chan *pubsub.NewMessage
}

// Init is a mock initialization method.
func (m *InMemoryPubsub) Init(metadata pubsub.Metadata) error {
	m.lock = &sync.Mutex{}
	args := m.Called(metadata)
	return args.Error(0)
}

// Publish is a mock publish method.
func (m *InMemoryPubsub) Publish(req *pubsub.PublishRequest) error {
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
