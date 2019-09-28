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
	"math/rand"
	"sync"
	"time"

	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"github.com/devigned/tab"
	"pack.ag/amqp"
)

type (
	// Sender provides connection, session and link handling for an sending to an entity path
	Sender struct {
		namespace          *Namespace
		client             *amqp.Client
		clientMu           sync.RWMutex
		session            *session
		sender             *amqp.Sender
		entityPath         string
		Name               string
		sessionID          *string
		doneRefreshingAuth func()
	}

	// SendOption provides a way to customize a message on sending
	SendOption func(event *Message) error

	eventer interface {
		toMsg() (*amqp.Message, error)
		GetKeyValues() map[string]interface{}
		Set(key string, value interface{})
	}

	// SenderOption provides a way to customize a Sender
	SenderOption func(*Sender) error
)

// NewSender creates a new Service Bus message Sender given an AMQP client and entity path
func (ns *Namespace) NewSender(ctx context.Context, entityPath string, opts ...SenderOption) (*Sender, error) {
	ctx, span := ns.startSpanFromContext(ctx, "sb.Namespace.NewSender")
	defer span.End()

	s := &Sender{
		namespace:  ns,
		entityPath: entityPath,
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}
	}

	err := s.newSessionAndLink(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
	}

	s.periodicallyRefreshAuth()

	return s, err
}

// Recover will attempt to close the current session and link, then rebuild them
func (s *Sender) Recover(ctx context.Context) error {
	ctx, span := s.startProducerSpanFromContext(ctx, "sb.Sender.Recover")
	defer span.End()

	// we expect the Sender, session or client is in an error state, ignore errors
	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	closeCtx = tab.NewContext(closeCtx, span)
	defer cancel()
	_ = s.Close(ctx)
	return s.newSessionAndLink(ctx)
}

// Close will close the AMQP connection, session and link of the Sender
func (s *Sender) Close(ctx context.Context) error {
	ctx, span := s.startProducerSpanFromContext(ctx, "sb.Sender.Close")
	defer span.End()

	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	if s.doneRefreshingAuth != nil {
		s.doneRefreshingAuth()
	}

	var lastErr error
	if s.sender != nil {
		if lastErr = s.sender.Close(ctx); lastErr != nil {
			tab.For(ctx).Error(lastErr)
		}
	}

	if s.session != nil {
		if err := s.session.Close(ctx); err != nil {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	if s.client != nil {
		if err := s.client.Close(); err != nil {
			tab.For(ctx).Error(err)
			lastErr = err
		}
	}

	s.sender = nil
	s.session = nil
	s.client = nil

	return lastErr
}

// Send will send a message to the entity path with options
//
// This will retry sending the message if the server responds with a busy error.
func (s *Sender) Send(ctx context.Context, msg *Message, opts ...SendOption) error {
	ctx, span := s.startProducerSpanFromContext(ctx, "sb.Sender.Send")
	defer span.End()

	if msg.SessionID == nil {
		msg.SessionID = &s.session.SessionID
		next := s.session.getNext()
		msg.GroupSequence = &next
	}

	if msg.ID == "" {
		id, err := uuid.NewV4()
		if err != nil {
			tab.For(ctx).Error(err)
			return err
		}
		msg.ID = id.String()
	}

	for _, opt := range opts {
		err := opt(msg)
		if err != nil {
			tab.For(ctx).Error(err)
			return err
		}
	}

	return s.trySend(ctx, msg)
}

func (s *Sender) trySend(ctx context.Context, evt eventer) error {
	ctx, sp := s.startProducerSpanFromContext(ctx, "sb.Sender.trySend")
	defer sp.End()

	err := sp.Inject(evt)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	msg, err := evt.toMsg()
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	if msg.Properties != nil {
		sp.AddAttributes(tab.StringAttribute("sb.message.id", msg.Properties.MessageID.(string)))
	}

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() != nil {
				tab.For(ctx).Error(err)
			}
			return ctx.Err()
		default:
			// try as long as the context is not dead
			err = s.sender.Send(ctx, msg)
			if err == nil {
				// successful send
				return err
			}

			switch err.(type) {
			case *amqp.Error, *amqp.DetachError:
				tab.For(ctx).Debug("amqp error, delaying 4 seconds: " + err.Error())
				skew := time.Duration(rand.Intn(1000)-500) * time.Millisecond
				time.Sleep(4*time.Second + skew)
				err := s.Recover(ctx)
				if err != nil {
					tab.For(ctx).Debug("failed to recover connection")
				}
				tab.For(ctx).Debug("recovered connection")
			default:
				tab.For(ctx).Error(err)
				return err
			}
		}
	}
}

func (s *Sender) String() string {
	return s.Name
}

func (s *Sender) getAddress() string {
	return s.entityPath
}

func (s *Sender) getFullIdentifier() string {
	return s.namespace.getEntityAudience(s.getAddress())
}

// newSessionAndLink will replace the existing session and link
func (s *Sender) newSessionAndLink(ctx context.Context) error {
	ctx, span := s.startProducerSpanFromContext(ctx, "sb.Sender.newSessionAndLink")
	defer span.End()

	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	connection, err := s.namespace.newClient()
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}
	s.client = connection

	err = s.namespace.negotiateClaim(ctx, connection, s.getAddress())
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	amqpSession, err := connection.NewSession()
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	amqpSender, err := amqpSession.NewSender(
		amqp.LinkSenderSettle(amqp.ModeUnsettled),
		amqp.LinkTargetAddress(s.getAddress()))
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	s.session, err = newSession(amqpSession)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}
	if s.sessionID != nil {
		s.session.SessionID = *s.sessionID
	}

	s.sender = amqpSender
	return nil
}

func (s *Sender) periodicallyRefreshAuth() {
	ctx, done := context.WithCancel(context.Background())
	s.doneRefreshingAuth = done

	ctx, span := s.startProducerSpanFromContext(ctx, "sb.Sender.periodicallyRefreshAuth")
	defer span.End()

	doNegotiateClaimLocked := func(ctx context.Context, r *Sender) {
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
				doNegotiateClaimLocked(ctx, s)
			}
		}
	}()
}

// SenderWithSession configures the message to send with a specific session and sequence. By default, a Sender has a
// default session (uuid.NewV4()) and sequence generator.
func SenderWithSession(sessionID *string) SenderOption {
	return func(sender *Sender) error {
		sender.sessionID = sessionID
		return nil
	}
}
