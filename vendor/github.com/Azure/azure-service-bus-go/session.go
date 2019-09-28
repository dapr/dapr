package servicebus

//	MIT License
//
//	Copyright (c) Microsoft Corporation. All rights reserved.
//
//	Permission is hereby granted, free of charge, to any person obtaining a copy
//	of this software and associated documentation files (the "Software"), to deal
//	in the Software without restriction, including without limitation the rights
//	to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//	copies of the Software, and to permit persons to whom the Software is
//	furnished to do so, subject to the following conditions:
//
//	The above copyright notice and this permission notice shall be included in all
//	copies or substantial portions of the Software.
//
//	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//	IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//	FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//	AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//	LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//	OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
//	SOFTWARE

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"github.com/devigned/tab"
	"pack.ag/amqp"
)

type (
	// session is a wrapper for the AMQP session with some added information to help with Service Bus messaging
	session struct {
		*amqp.Session
		SessionID string
		counter   uint32
	}

	sessionIdentifiable struct {
		sessionID *string
	}

	lockedRPC struct {
		rpcClient   *rpcClient
		rpcClientMu sync.Mutex
	}

	// QueueSession wraps Service Bus session functionality over a Queue
	QueueSession struct {
		sessionIdentifiable
		lockedRPC
		builder   SendAndReceiveBuilder
		builderMu sync.Mutex
		receiver  *Receiver
		sender    *Sender
	}

	// SubscriptionSession wraps Service Bus session functionality over a Subscription
	SubscriptionSession struct {
		sessionIdentifiable
		lockedRPC
		builder   ReceiveBuilder
		builderMu sync.Mutex
		receiver  *Receiver
	}

	// TopicSession wraps Service Bus session functionality over a Topic
	TopicSession struct {
		sessionIdentifiable
		builder   SenderBuilder
		builderMu sync.Mutex
		sender    *Sender
	}

	// ReceiverBuilder describes the ability of an entity to build receiver links
	ReceiverBuilder interface {
		NewReceiver(ctx context.Context, opts ...ReceiverOption) (*Receiver, error)
	}

	// SenderBuilder describes the ability of an entity to build sender links
	SenderBuilder interface {
		NewSender(ctx context.Context, opts ...SenderOption) (*Sender, error)
	}

	// EntityManagementAddresser describes the ability of an entity to provide an addressable path to it's management
	// endpoint
	EntityManagementAddresser interface {
		ManagementPath() string
	}

	// SendAndReceiveBuilder is a ReceiverBuilder, SenderBuilder and EntityManagementAddresser
	SendAndReceiveBuilder interface {
		ReceiveBuilder
		SenderBuilder
	}

	// ReceiveBuilder is a ReceiverBuilder and EntityManagementAddresser
	ReceiveBuilder interface {
		ReceiverBuilder
		entityConnector
	}
)

// newSession is a constructor for a Service Bus session which will pre-populate the SessionID with a new UUID
func newSession(amqpSession *amqp.Session) (*session, error) {
	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}

	return &session{
		Session:   amqpSession,
		SessionID: id.String(),
		counter:   0,
	}, nil
}

// getNext gets and increments the next group sequence number for the session
func (s *session) getNext() uint32 {
	return atomic.AddUint32(&s.counter, 1)
}

func (s *session) String() string {
	return s.SessionID
}

// NewQueueSession creates a new session sender and receiver to communicate with a Service Bus queue.
//
// Microsoft Azure Service Bus sessions enable joint and ordered handling of unbounded sequences of related messages.
// To realize a FIFO guarantee in Service Bus, use Sessions. Service Bus is not prescriptive about the nature of the
// relationship between the messages, and also does not define a particular model for determining where a message
// sequence starts or ends.
func NewQueueSession(builder SendAndReceiveBuilder, sessionID *string) *QueueSession {
	return &QueueSession{
		sessionIdentifiable: sessionIdentifiable{
			sessionID: sessionID,
		},
		builder: builder,
	}
}

// ReceiveOne waits for the lock on a particular session to become available, takes it, then process the session.
// The session can contain multiple messages. ReceiveOne will receive all messages within that session.
//
// Handler must call a disposition action such as Complete, Abandon, Deadletter on the message. If the messages does not
// have a disposition set, the Queue's DefaultDisposition will be used.
//
// If the handler returns an error, the receive loop will be terminated.
func (qs *QueueSession) ReceiveOne(ctx context.Context, handler SessionHandler) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.ReceiveOne")
	defer span.End()

	if err := qs.ensureReceiver(ctx); err != nil {
		return err
	}

	ms, err := newMessageSession(qs.receiver, qs.builder, qs.sessionID)
	if err != nil {
		return err
	}

	err = handler.Start(ms)
	if err != nil {
		return err
	}

	defer handler.End()
	handle := qs.receiver.Listen(ctx, handler)

	select {
	case <-handle.Done():
		return handle.Err()
	case <-ms.done:
		return nil
	}
}

// ReceiveDeferred will receive and handle a set of deferred messages
//
// When a queue or subscription client receives a message that it is willing to process, but for which processing is
// not currently possible due to special circumstances inside of the application, it has the option of "deferring"
// retrieval of the message to a later point. The message remains in the queue or subscription, but it is set aside.
//
// Deferral is a feature specifically created for workflow processing scenarios. Workflow frameworks may require certain
// operations to be processed in a particular order, and may have to postpone processing of some received messages
// until prescribed prior work that is informed by other messages has been completed.
//
// A simple illustrative example is an order processing sequence in which a payment notification from an external
// payment provider appears in a system before the matching purchase order has been propagated from the store front
// to the fulfillment system. In that case, the fulfillment system might defer processing the payment notification
// until there is an order with which to associate it. In rendezvous scenarios, where messages from different sources
// drive a workflow forward, the real-time execution order may indeed be correct, but the messages reflecting the
// outcomes may arrive out of order.
//
// Ultimately, deferral aids in reordering messages from the arrival order into an order in which they can be
// processed, while leaving those messages safely in the message store for which processing needs to be postponed.
func (qs *QueueSession) ReceiveDeferred(ctx context.Context, handler Handler, mode ReceiveMode, sequenceNumbers ...int64) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.Queue.ReceiveDeferred")
	defer span.End()

	if err := qs.ensureRPCClient(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	messages, err := qs.rpcClient.ReceiveDeferred(ctx, mode, sequenceNumbers...)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	for _, msg := range messages {
		if err := handler.Handle(ctx, msg); err != nil {
			tab.For(ctx).Error(err)
			return err
		}
	}
	return nil
}

// Send the message to the queue within a session
func (qs *QueueSession) Send(ctx context.Context, msg *Message) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.Send")
	defer span.End()

	if err := qs.ensureSender(ctx); err != nil {
		return err
	}

	if msg.SessionID == nil {
		msg.SessionID = qs.sessionID
	}
	return qs.sender.Send(ctx, msg)
}

// Close the underlying connection to Service Bus
func (qs *QueueSession) Close(ctx context.Context) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.Close")
	defer span.End()

	var lastErr error
	if qs.receiver != nil {
		if err := qs.receiver.Close(ctx); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	if qs.sender != nil {
		if err := qs.sender.Close(ctx); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	if qs.rpcClient != nil {
		if err := qs.rpcClient.Close(); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	return lastErr
}

// SessionID is the identifier for the Service Bus session
func (qs *QueueSession) SessionID() *string {
	return qs.sessionID
}

// ManagementPath provides an addressable path to the Entity management endpoint
func (qs *QueueSession) ManagementPath() string {
	return qs.builder.ManagementPath()
}

func (qs *QueueSession) ensureRPCClient(ctx context.Context) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.ensureRPCConn")
	defer span.End()

	qs.rpcClientMu.Lock()
	defer qs.rpcClientMu.Unlock()

	if qs.rpcClient != nil {
		return nil
	}

	client, err := newRPCClient(ctx, qs.builder, rpcClientWithSession(qs.sessionID))
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	qs.rpcClient = client
	return nil
}

func (qs *QueueSession) ensureSender(ctx context.Context) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.ensureSender")
	defer span.End()

	qs.builderMu.Lock()
	defer qs.builderMu.Unlock()

	if qs.sender != nil {
		return nil
	}

	s, err := qs.builder.NewSender(ctx, SenderWithSession(qs.sessionID))
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	qs.sender = s
	return nil
}

func (qs *QueueSession) ensureReceiver(ctx context.Context) error {
	ctx, span := qs.startSpanFromContext(ctx, "sb.QueueSession.ensureReceiver")
	defer span.End()

	qs.builderMu.Lock()
	defer qs.builderMu.Unlock()

	if qs.receiver != nil {
		return nil
	}

	r, err := qs.builder.NewReceiver(ctx, ReceiverWithSession(qs.sessionID))
	if err != nil {
		return err
	}

	qs.receiver = r
	return nil
}

// NewSubscriptionSession creates a new session receiver to receive from a Service Bus subscription.
//
// Microsoft Azure Service Bus sessions enable joint and ordered handling of unbounded sequences of related messages.
// To realize a FIFO guarantee in Service Bus, use Sessions. Service Bus is not prescriptive about the nature of the
// relationship between the messages, and also does not define a particular model for determining where a message
// sequence starts or ends.
func NewSubscriptionSession(builder ReceiveBuilder, sessionID *string) *SubscriptionSession {
	return &SubscriptionSession{
		sessionIdentifiable: sessionIdentifiable{
			sessionID: sessionID,
		},
		builder: builder,
	}
}

// ReceiveOne waits for the lock on a particular session to become available, takes it, then process the session.
// The session can contain multiple messages. ReceiveOneSession will receive all messages within that session.
//
// Handler must call a disposition action such as Complete, Abandon, Deadletter on the message. If the messages does not
// have a disposition set, the Queue's DefaultDisposition will be used.
//
// If the handler returns an error, the receive loop will be terminated.
func (ss *SubscriptionSession) ReceiveOne(ctx context.Context, handler SessionHandler) error {
	ctx, span := ss.startSpanFromContext(ctx, "sb.SubscriptionSession.ReceiveOne")
	defer span.End()

	if err := ss.ensureReceiver(ctx); err != nil {
		return err
	}

	ms, err := newMessageSession(ss.receiver, ss.builder, ss.sessionID)
	if err != nil {
		return err
	}

	err = handler.Start(ms)
	if err != nil {
		return err
	}

	defer handler.End()
	handle := ss.receiver.Listen(ctx, handler)

	select {
	case <-handle.Done():
		err := handle.Err()
		if err != nil {
			tab.For(ctx).Error(err)
			_ = ss.receiver.Close(ctx)
		}
		return err
	case <-ms.done:
		return nil
	}
}

// ReceiveDeferred will receive and handle a set of deferred messages
//
// When a queue or subscription client receives a message that it is willing to process, but for which processing is
// not currently possible due to special circumstances inside of the application, it has the option of "deferring"
// retrieval of the message to a later point. The message remains in the queue or subscription, but it is set aside.
//
// Deferral is a feature specifically created for workflow processing scenarios. Workflow frameworks may require certain
// operations to be processed in a particular order, and may have to postpone processing of some received messages
// until prescribed prior work that is informed by other messages has been completed.
//
// A simple illustrative example is an order processing sequence in which a payment notification from an external
// payment provider appears in a system before the matching purchase order has been propagated from the store front
// to the fulfillment system. In that case, the fulfillment system might defer processing the payment notification
// until there is an order with which to associate it. In rendezvous scenarios, where messages from different sources
// drive a workflow forward, the real-time execution order may indeed be correct, but the messages reflecting the
// outcomes may arrive out of order.
//
// Ultimately, deferral aids in reordering messages from the arrival order into an order in which they can be
// processed, while leaving those messages safely in the message store for which processing needs to be postponed.
func (ss *SubscriptionSession) ReceiveDeferred(ctx context.Context, handler Handler, mode ReceiveMode, sequenceNumbers ...int64) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.Queue.ReceiveDeferred")
	defer span.End()

	if err := ss.ensureRPCClient(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	messages, err := ss.rpcClient.ReceiveDeferred(ctx, mode, sequenceNumbers...)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	for _, msg := range messages {
		if err := handler.Handle(ctx, msg); err != nil {
			tab.For(ctx).Error(err)
			return err
		}
	}
	return nil
}

// Close the underlying connection to Service Bus
func (ss *SubscriptionSession) Close(ctx context.Context) error {
	ctx, span := ss.startSpanFromContext(ctx, "sb.SubscriptionSession.Close")
	defer span.End()

	var lastErr error
	if ss.receiver != nil {
		if err := ss.receiver.Close(ctx); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	if ss.rpcClient != nil {
		if err := ss.rpcClient.Close(); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	return lastErr
}

func (ss *SubscriptionSession) ensureReceiver(ctx context.Context) error {
	ctx, span := ss.startSpanFromContext(ctx, "sb.SubscriptionSession.ensureReceiver")
	defer span.End()

	ss.builderMu.Lock()
	defer ss.builderMu.Unlock()

	r, err := ss.builder.NewReceiver(ctx, ReceiverWithSession(ss.sessionID))
	if err != nil {
		return err
	}

	ss.receiver = r
	return nil
}

// SessionID is the identifier for the Service Bus session
func (ss *SubscriptionSession) SessionID() *string {
	return ss.sessionID
}

// ManagementPath provides an addressable path to the Entity management endpoint
func (ss *SubscriptionSession) ManagementPath() string {
	return ss.builder.ManagementPath()
}

func (ss *SubscriptionSession) ensureRPCClient(ctx context.Context) error {
	ctx, span := ss.startSpanFromContext(ctx, "sb.SubscriptionSession.ensureRpcConn")
	defer span.End()

	ss.rpcClientMu.Lock()
	defer ss.rpcClientMu.Unlock()

	if ss.rpcClient != nil {
		return nil
	}

	client, err := newRPCClient(ctx, ss.builder, rpcClientWithSession(ss.sessionID))
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	ss.rpcClient = client
	return nil
}

// NewTopicSession creates a new session receiver to receive from a Service Bus topic.
//
// Microsoft Azure Service Bus sessions enable joint and ordered handling of unbounded sequences of related messages.
// To realize a FIFO guarantee in Service Bus, use Sessions. Service Bus is not prescriptive about the nature of the
// relationship between the messages, and also does not define a particular model for determining where a message
// sequence starts or ends.
func NewTopicSession(builder SenderBuilder, sessionID *string) *TopicSession {
	return &TopicSession{
		sessionIdentifiable: sessionIdentifiable{
			sessionID: sessionID,
		},
		builder: builder,
	}
}

// Send the message to the queue within a session
func (ts *TopicSession) Send(ctx context.Context, msg *Message) error {
	ctx, span := ts.startSpanFromContext(ctx, "sb.TopicSession.Send")
	defer span.End()

	if err := ts.ensureSender(ctx); err != nil {
		return err
	}

	if msg.SessionID == nil {
		msg.SessionID = ts.sessionID
	}
	return ts.sender.Send(ctx, msg)
}

// Close the underlying connection to Service Bus
func (ts *TopicSession) Close(ctx context.Context) error {
	ctx, span := ts.startSpanFromContext(ctx, "sb.TopicSession.Close")
	defer span.End()

	if ts.sender != nil {
		if err := ts.sender.Close(ctx); err != nil {
			tab.For(ctx).Error(err)
			return err
		}
	}
	return nil
}

// SessionID is the identifier for the Service Bus session
func (ts *TopicSession) SessionID() *string {
	return ts.sessionID
}

func (ts *TopicSession) ensureSender(ctx context.Context) error {
	ctx, span := ts.startSpanFromContext(ctx, "sb.TopicSession.ensureSender")
	defer span.End()

	ts.builderMu.Lock()
	defer ts.builderMu.Unlock()

	s, err := ts.builder.NewSender(ctx, SenderWithSession(ts.sessionID))
	if err != nil {
		return err
	}

	ts.sender = s
	return nil
}
