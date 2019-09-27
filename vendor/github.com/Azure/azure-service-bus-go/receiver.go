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
	"time"

	"github.com/Azure/azure-amqp-common-go/v2"
	"github.com/devigned/tab"
	"pack.ag/amqp"
)

type (
	// Receiver provides connection, session and link handling for a receiving to an entity path
	Receiver struct {
		namespace          *Namespace
		client             *amqp.Client
		clientMu           sync.RWMutex
		session            *session
		receiver           *amqp.Receiver
		entityPath         string
		doneListening      func()
		Name               string
		useSessions        bool
		sessionID          *string
		lastError          error
		mode               ReceiveMode
		prefetch           uint32
		DefaultDisposition DispositionAction
		Closed             bool
		doneRefreshingAuth func()
	}

	// ReceiverOption provides a structure for configuring receivers
	ReceiverOption func(receiver *Receiver) error

	// ListenerHandle provides the ability to close or listen to the close of a Receiver
	ListenerHandle struct {
		r   *Receiver
		ctx context.Context
	}
)

// ReceiverWithSession configures a Receiver to use a session
func ReceiverWithSession(sessionID *string) ReceiverOption {
	return func(r *Receiver) error {
		r.sessionID = sessionID
		r.useSessions = true
		return nil
	}
}

// ReceiverWithReceiveMode configures a Receiver to use the specified receive mode
func ReceiverWithReceiveMode(mode ReceiveMode) ReceiverOption {
	return func(r *Receiver) error {
		r.mode = mode
		return nil
	}
}

// ReceiverWithPrefetchCount configures the receiver to attempt to fetch the number of messages specified by the prefect
// at one time.
//
// The default is 1 message at a time.
//
// Caution: Using PeekLock, messages have a set lock timeout, which can be renewed. By setting a high prefetch count, a
// local queue of messages could build up and cause message locks to expire before the message lands in the handler. If
// this happens, the message disposition will fail and will be re-queued and processed again.
func ReceiverWithPrefetchCount(prefetch uint32) ReceiverOption {
	return func(receiver *Receiver) error {
		receiver.prefetch = prefetch
		return nil
	}
}

// NewReceiver creates a new Service Bus message listener given an AMQP client and an entity path
func (ns *Namespace) NewReceiver(ctx context.Context, entityPath string, opts ...ReceiverOption) (*Receiver, error) {
	ctx, span := ns.startSpanFromContext(ctx, "sb.Namespace.NewReceiver")
	defer span.End()

	r := &Receiver{
		namespace:  ns,
		entityPath: entityPath,
		mode:       PeekLockMode,
		prefetch:   1,
	}

	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}

	err := r.newSessionAndLink(ctx)
	if err != nil {
		_ = r.Close(ctx)
		return nil, err
	}

	r.periodicallyRefreshAuth()

	return r, nil
}

// Close will close the AMQP session and link of the Receiver
func (r *Receiver) Close(ctx context.Context) error {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.Close")
	defer span.End()

	r.clientMu.Lock()
	defer r.clientMu.Unlock()

	if r.doneListening != nil {
		r.doneListening()
	}

	if r.doneRefreshingAuth != nil {
		r.doneRefreshingAuth()
	}

	r.Closed = true

	var lastErr error
	if r.receiver != nil {
		lastErr = r.receiver.Close(ctx)
		if lastErr != nil {
			tab.For(ctx).Error(lastErr)
		}
	}

	if r.session != nil {
		if err := r.session.Close(ctx); err != nil {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	if r.client != nil {
		if err := r.client.Close(); err != nil {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	r.receiver = nil
	r.session = nil
	r.client = nil

	return lastErr
}

// Recover will attempt to close the current session and link, then rebuild them
func (r *Receiver) Recover(ctx context.Context) error {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.Recover")
	defer span.End()

	// we expect the Sender, session or client is in an error state, ignore errors
	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	closeCtx = tab.NewContext(closeCtx, span)
	defer cancel()
	_ = r.Close(ctx)
	return r.newSessionAndLink(ctx)
}

// ReceiveOne will receive one message from the link
func (r *Receiver) ReceiveOne(ctx context.Context, handler Handler) error {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.ReceiveOne")
	defer span.End()

	amqpMsg, err := r.listenForMessage(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.handleMessage(ctx, amqpMsg, handler)

	return nil
}

// Listen start a listener for messages sent to the entity path
func (r *Receiver) Listen(ctx context.Context, handler Handler) *ListenerHandle {
	ctx, done := context.WithCancel(ctx)
	r.doneListening = done

	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.Listen")
	defer span.End()

	messages := make(chan *amqp.Message)
	go r.listenForMessages(ctx, messages)
	go r.handleMessages(ctx, messages, handler)

	return &ListenerHandle{
		r:   r,
		ctx: ctx,
	}
}

func (r *Receiver) handleMessages(ctx context.Context, messages chan *amqp.Message, handler Handler) {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.handleMessages")
	defer span.End()
	for msg := range messages {
		r.handleMessage(ctx, msg, handler)
	}
}

func (r *Receiver) handleMessage(ctx context.Context, msg *amqp.Message, handler Handler) {
	const optName = "sb.Receiver.handleMessage"

	event, err := messageFromAMQPMessage(msg)
	if err != nil {
		_, span := r.startConsumerSpanFromContext(ctx, optName)
		span.Logger().Error(err)
		r.lastError = err
		r.doneListening()
		return
	}

	ctx, span := tab.StartSpanWithRemoteParent(ctx, optName, event)
	defer span.End()

	id := messageID(msg)
	if idStr, ok := id.(string); ok {
		span.AddAttributes(tab.StringAttribute("amqp.message.id", idStr))
	}

	if err := handler.Handle(ctx, event); err != nil {
		// stop handling messages since the message consumer ran into an unexpected error
		r.lastError = err
		r.doneListening()
		return
	}

	// nothing more to be done. The message was settled when it was accepted by the Receiver
	if r.mode == ReceiveAndDeleteMode {
		return
	}

	// nothing more to be done. The Receiver has no default disposition, so the handler is solely responsible for
	// disposition
	if r.DefaultDisposition == nil {
		return
	}

	// default disposition is set, so try to send the disposition. If the message disposition has already been set, the
	// underlying AMQP library will ignore the second disposition respecting the disposition of the handler func.
	if err := r.DefaultDisposition(ctx); err != nil {
		// if an error is returned by the default disposition, then we must alert the message consumer as we can't
		// be sure the final message disposition.
		tab.For(ctx).Error(err)
		r.lastError = err
		r.doneListening()
		return
	}
}

func (r *Receiver) listenForMessages(ctx context.Context, msgChan chan *amqp.Message) {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.listenForMessages")
	defer span.End()

	for {
		msg, err := r.listenForMessage(ctx)
		if err == nil {
			msgChan <- msg
			continue
		}

		select {
		case <-ctx.Done():
			tab.For(ctx).Debug("context done")
			close(msgChan)
			return
		default:
			_, retryErr := common.Retry(10, 10*time.Second, func() (interface{}, error) {
				ctx, sp := r.startConsumerSpanFromContext(ctx, "sb.Receiver.listenForMessages.tryRecover")
				defer sp.End()

				tab.For(ctx).Debug("recovering connection")
				err := r.Recover(ctx)
				if err == nil {
					tab.For(ctx).Debug("recovered connection")
					return nil, nil
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					return nil, common.Retryable(err.Error())
				}
			})

			if retryErr != nil {
				tab.For(ctx).Debug("retried, but error was unrecoverable")
				r.lastError = retryErr
				if err := r.Close(ctx); err != nil {
					tab.For(ctx).Error(err)
				}
				close(msgChan)
				return
			}
		}
	}
}

func (r *Receiver) listenForMessage(ctx context.Context) (*amqp.Message, error) {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.listenForMessage")
	defer span.End()

	msg, err := r.receiver.Receive(ctx)
	if err != nil {
		tab.For(ctx).Debug(err.Error())
		return nil, err
	}

	id := messageID(msg)
	if idStr, ok := id.(string); ok {
		span.AddAttributes(tab.StringAttribute("amqp.message.id", idStr))
	}

	return msg, nil
}

// newSessionAndLink will replace the session and link on the Receiver
func (r *Receiver) newSessionAndLink(ctx context.Context) error {
	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.newSessionAndLink")
	defer span.End()

	r.clientMu.Lock()
	defer r.clientMu.Unlock()

	client, err := r.namespace.newClient()
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}
	r.client = client

	err = r.namespace.negotiateClaim(ctx, client, r.entityPath)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	amqpSession, err := client.NewSession()
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.session, err = newSession(amqpSession)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	receiveMode := amqp.ModeSecond
	if r.mode == ReceiveAndDeleteMode {
		receiveMode = amqp.ModeFirst
	}

	opts := []amqp.LinkOption{
		amqp.LinkSourceAddress(r.entityPath),
		amqp.LinkReceiverSettle(receiveMode),
		amqp.LinkCredit(r.prefetch),
	}

	if r.mode == ReceiveAndDeleteMode {
		opts = append(opts, amqp.LinkSenderSettle(amqp.ModeSettled))
	}

	if opt, ok := r.getSessionFilterLinkOption(); ok {
		opts = append(opts, opt)
	}

	amqpReceiver, err := amqpSession.NewReceiver(opts...)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.receiver = amqpReceiver
	return nil
}

func (r *Receiver) getSessionFilterLinkOption() (amqp.LinkOption, bool) {
	const name = "com.microsoft:session-filter"
	const code = uint64(0x00000137000000C)

	if !r.useSessions {
		return nil, false
	}

	if r.sessionID == nil {
		return amqp.LinkSourceFilter(name, code, nil), true
	}

	return amqp.LinkSourceFilter(name, code, r.sessionID), true
}

func (r *Receiver) periodicallyRefreshAuth() {
	ctx, done := context.WithCancel(context.Background())
	r.doneRefreshingAuth = done

	ctx, span := r.startConsumerSpanFromContext(ctx, "sb.Receiver.periodicallyRefreshAuth")
	defer span.End()

	doNegotiateClaimLocked := func(ctx context.Context, r *Receiver) {
		r.clientMu.RLock()
		defer r.clientMu.RUnlock()

		if r.client != nil {
			if err := r.namespace.negotiateClaim(ctx, r.client, r.entityPath); err != nil {
				tab.For(ctx).Error(err)
			}
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(5 * time.Minute)
				doNegotiateClaimLocked(ctx, r)
			}
		}
	}()
}

func messageID(msg *amqp.Message) interface{} {
	var id interface{} = "null"
	if msg.Properties != nil {
		id = msg.Properties.MessageID
	}
	return id
}

// Close will close the listener
func (lc *ListenerHandle) Close(ctx context.Context) error {
	return lc.r.Close(ctx)
}

// Done will close the channel when the listener has stopped
func (lc *ListenerHandle) Done() <-chan struct{} {
	return lc.ctx.Done()
}

// Err will return the last error encountered
func (lc *ListenerHandle) Err() error {
	if lc.r.lastError != nil {
		return lc.r.lastError
	}
	return lc.ctx.Err()
}
