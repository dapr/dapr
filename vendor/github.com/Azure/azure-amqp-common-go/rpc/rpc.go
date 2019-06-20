// Package rpc provides functionality for request / reply messaging. It is used by package mgmt and cbs.
package rpc

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
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-amqp-common-go"
	"github.com/Azure/azure-amqp-common-go/internal/tracing"
	"github.com/Azure/azure-amqp-common-go/log"
	"github.com/Azure/azure-amqp-common-go/uuid"
	"pack.ag/amqp"
)

const (
	replyPostfix   = "-reply-to-"
	statusCodeKey  = "status-code"
	descriptionKey = "status-description"
)

type (
	// Link is the bidirectional communication structure used for CBS negotiation
	Link struct {
		session       *amqp.Session
		receiver      *amqp.Receiver
		sender        *amqp.Sender
		clientAddress string
		rpcMu         sync.Mutex
		id            string
	}

	// Response is the simplified response structure from an RPC like call
	Response struct {
		Code        int
		Description string
		Message     *amqp.Message
	}
)

// NewLink will build a new request response link
func NewLink(conn *amqp.Client, address string) (*Link, error) {
	authSession, err := conn.NewSession()
	if err != nil {
		return nil, err
	}

	return NewLinkWithSession(conn, authSession, address)
}

// NewLinkWithSession will build a new request response link, but will reuse an existing AMQP session
func NewLinkWithSession(conn *amqp.Client, session *amqp.Session, address string) (*Link, error) {

	authSender, err := session.NewSender(
		amqp.LinkTargetAddress(address),
	)
	if err != nil {
		return nil, err
	}

	linkID, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}

	id := linkID.String()
	clientAddress := strings.Replace("$", "", address, -1) + replyPostfix + id
	authReceiver, err := session.NewReceiver(
		amqp.LinkSourceAddress(address),
		amqp.LinkTargetAddress(clientAddress),
	)
	if err != nil {
		return nil, err
	}

	return &Link{
		sender:        authSender,
		receiver:      authReceiver,
		session:       session,
		clientAddress: clientAddress,
		id:            id,
	}, nil
}

// RetryableRPC attempts to retry a request a number of times with delay
func (l *Link) RetryableRPC(ctx context.Context, times int, delay time.Duration, msg *amqp.Message) (*Response, error) {
	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.RetryableRPC")
	defer span.End()

	res, err := common.Retry(times, delay, func() (interface{}, error) {
		span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.RetryableRPC.retry")
		defer span.End()

		res, err := l.RPC(ctx, msg)
		if err != nil {
			log.For(ctx).Error(fmt.Errorf("error in RPC via link %s: %v", l.id, err))
			return nil, err
		}

		switch {
		case res.Code >= 200 && res.Code < 300:
			log.For(ctx).Debug(fmt.Sprintf("successful rpc on link %s: status code %d and description: %s", l.id, res.Code, res.Description))
			return res, nil
		case res.Code >= 500:
			errMessage := fmt.Sprintf("server error link %s: status code %d and description: %s", l.id, res.Code, res.Description)
			log.For(ctx).Error(errors.New(errMessage))
			return nil, common.Retryable(errMessage)
		default:
			errMessage := fmt.Sprintf("unhandled error link %s: status code %d and description: %s", l.id, res.Code, res.Description)
			log.For(ctx).Error(errors.New(errMessage))
			return nil, common.Retryable(errMessage)
		}
	})
	if err != nil {
		return nil, err
	}
	return res.(*Response), nil
}

// RPC sends a request and waits on a response for that request
func (l *Link) RPC(ctx context.Context, msg *amqp.Message) (*Response, error) {
	const altStatusCodeKey, altDescriptionKey = "statusCode", "statusDescription"

	l.rpcMu.Lock()
	defer l.rpcMu.Unlock()

	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.RPC")
	defer span.End()

	if msg.Properties == nil {
		msg.Properties = &amqp.MessageProperties{}
	}
	msg.Properties.ReplyTo = l.clientAddress

	if msg.ApplicationProperties == nil {
		msg.ApplicationProperties = make(map[string]interface{})
	}

	if _, ok := msg.ApplicationProperties["server-timeout"]; !ok {
		if deadline, ok := ctx.Deadline(); ok {
			msg.ApplicationProperties["server-timeout"] = uint(time.Until(deadline) / time.Millisecond)
		}
	}

	err := l.sender.Send(ctx, msg)
	if err != nil {
		return nil, err
	}

	res, err := l.receiver.Receive(ctx)
	if err != nil {
		return nil, err
	}

	var statusCode int
	statusCodeCandidates := []string{statusCodeKey, altStatusCodeKey}
	for i := range statusCodeCandidates {
		if rawStatusCode, ok := res.ApplicationProperties[statusCodeCandidates[i]]; ok {
			if cast, ok := rawStatusCode.(int32); ok {
				statusCode = int(cast)
				break
			} else {
				return nil, errors.New("status code was not of expected type int32")
			}
		}
	}
	if statusCode == 0 {
		return nil, errors.New("status codes was not found on rpc message")
	}

	var description string
	descriptionCandidates := []string{descriptionKey, altDescriptionKey}
	for i := range descriptionCandidates {
		if rawDescription, ok := res.ApplicationProperties[descriptionCandidates[i]]; ok {
			if description, ok = rawDescription.(string); ok || rawDescription == nil {
				break
			} else {
				return nil, errors.New("status description was not of expected type string")
			}
		}
	}

	res.Accept()
	return &Response{
		Code:        int(statusCode),
		Description: description,
		Message:     res,
	}, err
}

// Close the link receiver, sender and session
func (l *Link) Close(ctx context.Context) error {
	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.Close")
	defer span.End()

	if err := l.closeReceiver(ctx); err != nil {
		_ = l.closeSender(ctx)
		_ = l.closeSession(ctx)
		return err
	}

	if err := l.closeSender(ctx); err != nil {
		_ = l.closeSession(ctx)
		return err
	}

	return l.closeSession(ctx)
}

func (l *Link) closeReceiver(ctx context.Context) error {
	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.closeReceiver")
	defer span.End()

	if l.receiver != nil {
		return l.receiver.Close(ctx)
	}
	return nil
}

func (l *Link) closeSender(ctx context.Context) error {
	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.closeSender")
	defer span.End()

	if l.sender != nil {
		return l.sender.Close(ctx)
	}
	return nil
}

func (l *Link) closeSession(ctx context.Context) error {
	span, ctx := tracing.StartSpanFromContext(ctx, "az-amqp-common.rpc.closeSession")
	defer span.End()

	if l.session != nil {
		return l.session.Close(ctx)
	}
	return nil
}
