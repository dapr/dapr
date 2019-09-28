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
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Azure/azure-amqp-common-go/v2/rpc"
	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"github.com/devigned/tab"
	"pack.ag/amqp"
)

type (
	rpcClient struct {
		ec                 entityConnector
		client             *amqp.Client
		clientMu           sync.RWMutex
		sessionID          *string
		isSessionFilterSet bool
		doneRefreshingAuth func()
	}

	rpcClientOption func(*rpcClient) error
)

func newRPCClient(ctx context.Context, ec entityConnector, opts ...rpcClientOption) (*rpcClient, error) {
	r := &rpcClient{
		ec: ec,
	}

	for _, opt := range opts {
		if err := opt(r); err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}
	}

	return r, nil
}

// Recover will attempt to close the current session and link, then rebuild them
func (r *rpcClient) Recover(ctx context.Context) error {
	ctx, span := r.startSpanFromContext(ctx, "sb.rpcClient.Recover")
	defer span.End()

	_ = r.Close()
	return r.ensureConn(ctx)
}

// Close will close the AMQP connection
func (r *rpcClient) Close() error {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()

	return r.client.Close()
}

func (r *rpcClient) ensureConn(ctx context.Context) error {
	ctx, span := r.startSpanFromContext(ctx, "sb.rpcClient.ensureConn")
	defer span.End()

	if r.client != nil {
		return nil
	}

	r.clientMu.Lock()
	defer r.clientMu.Unlock()

	client, err := r.ec.Namespace().newClient()
	err = r.ec.Namespace().negotiateClaim(ctx, client, r.ec.ManagementPath())
	if err != nil {
		tab.For(ctx).Error(err)
		_ = client.Close()
		return err
	}

	r.client = client
	return err
}

func (r *rpcClient) ReceiveDeferred(ctx context.Context, mode ReceiveMode, sequenceNumbers ...int64) ([]*Message, error) {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.rpcClient.ReceiveDeferred")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	const messagesField, messageField = "messages", "message"

	backwardsMode := uint32(0)
	if mode == PeekLockMode {
		backwardsMode = 1
	}

	values := map[string]interface{}{
		"sequence-numbers":     sequenceNumbers,
		"receiver-settle-mode": uint32(backwardsMode), // pick up messages with peek lock
	}

	var opts []rpc.LinkOption
	if r.isSessionFilterSet {
		opts = append(opts, rpc.LinkWithSessionFilter(r.sessionID))
		values["session-id"] = r.sessionID
	}

	link, err := rpc.NewLink(r.client, r.ec.ManagementPath(), opts...)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: "com.microsoft:receive-by-sequence-number",
		},
		Value: values,
	}

	rsp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	if rsp.Code == 204 {
		return nil, ErrNoMessages{}
	}

	// Deferred messages come back in a relatively convoluted manner:
	// a map (always with one key: "messages")
	// 	of arrays
	// 		of maps (always with one key: "message")
	// 			of an array with raw encoded Service Bus messages
	val, ok := rsp.Message.Value.(map[string]interface{})
	if !ok {
		return nil, newErrIncorrectType(messageField, map[string]interface{}{}, rsp.Message.Value)
	}

	rawMessages, ok := val[messagesField]
	if !ok {
		return nil, ErrMissingField(messagesField)
	}

	messages, ok := rawMessages.([]interface{})
	if !ok {
		return nil, newErrIncorrectType(messagesField, []interface{}{}, rawMessages)
	}

	transformedMessages := make([]*Message, len(messages))
	for i := range messages {
		rawEntry, ok := messages[i].(map[string]interface{})
		if !ok {
			return nil, newErrIncorrectType(messageField, map[string]interface{}{}, messages[i])
		}

		rawMessage, ok := rawEntry[messageField]
		if !ok {
			return nil, ErrMissingField(messageField)
		}

		marshaled, ok := rawMessage.([]byte)
		if !ok {
			return nil, new(ErrMalformedMessage)
		}

		var rehydrated amqp.Message
		err = rehydrated.UnmarshalBinary(marshaled)
		if err != nil {
			return nil, err
		}

		transformedMessages[i], err = messageFromAMQPMessage(&rehydrated)
		if err != nil {
			return nil, err
		}

		transformedMessages[i].ec = r.ec
		transformedMessages[i].useSession = r.isSessionFilterSet
		transformedMessages[i].sessionID = r.sessionID
	}

	// This sort is done to ensure that folks wanting to peek messages in sequence order may do so.
	sort.Slice(transformedMessages, func(i, j int) bool {
		iSeq := *transformedMessages[i].SystemProperties.SequenceNumber
		jSeq := *transformedMessages[j].SystemProperties.SequenceNumber
		return iSeq < jSeq
	})

	return transformedMessages, nil
}

func (r *rpcClient) GetNextPage(ctx context.Context, fromSequenceNumber int64, messageCount int32) ([]*Message, error) {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.rpcClient.GetNextPage")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	const messagesField, messageField = "messages", "message"

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: peekMessageOperationID,
		},
		Value: map[string]interface{}{
			"from-sequence-number": fromSequenceNumber,
			"message-count":        messageCount,
		},
	}

	if deadline, ok := ctx.Deadline(); ok {
		msg.ApplicationProperties["server-timeout"] = uint(time.Until(deadline) / time.Millisecond)
	}

	link, err := rpc.NewLink(r.client, r.ec.ManagementPath())
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	rsp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	if rsp.Code == 204 {
		return nil, ErrNoMessages{}
	}

	// Peeked messages come back in a relatively convoluted manner:
	// a map (always with one key: "messages")
	// 	of arrays
	// 		of maps (always with one key: "message")
	// 			of an array with raw encoded Service Bus messages
	val, ok := rsp.Message.Value.(map[string]interface{})
	if !ok {
		err = newErrIncorrectType(messageField, map[string]interface{}{}, rsp.Message.Value)
		tab.For(ctx).Error(err)
		return nil, err
	}

	rawMessages, ok := val[messagesField]
	if !ok {
		err = ErrMissingField(messagesField)
		tab.For(ctx).Error(err)
		return nil, err
	}

	messages, ok := rawMessages.([]interface{})
	if !ok {
		err = newErrIncorrectType(messagesField, []interface{}{}, rawMessages)
		tab.For(ctx).Error(err)
		return nil, err
	}

	transformedMessages := make([]*Message, len(messages))
	for i := range messages {
		rawEntry, ok := messages[i].(map[string]interface{})
		if !ok {
			err = newErrIncorrectType(messageField, map[string]interface{}{}, messages[i])
			tab.For(ctx).Error(err)
			return nil, err
		}

		rawMessage, ok := rawEntry[messageField]
		if !ok {
			err = ErrMissingField(messageField)
			tab.For(ctx).Error(err)
			return nil, err
		}

		marshaled, ok := rawMessage.([]byte)
		if !ok {
			err = new(ErrMalformedMessage)
			tab.For(ctx).Error(err)
			return nil, err
		}

		var rehydrated amqp.Message
		err = rehydrated.UnmarshalBinary(marshaled)
		if err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}

		transformedMessages[i], err = messageFromAMQPMessage(&rehydrated)
		if err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}

		transformedMessages[i].ec = r.ec
		transformedMessages[i].useSession = r.isSessionFilterSet
		transformedMessages[i].sessionID = r.sessionID
	}

	// This sort is done to ensure that folks wanting to peek messages in sequence order may do so.
	sort.Slice(transformedMessages, func(i, j int) bool {
		iSeq := *transformedMessages[i].SystemProperties.SequenceNumber
		jSeq := *transformedMessages[j].SystemProperties.SequenceNumber
		return iSeq < jSeq
	})

	return transformedMessages, nil
}

func (r *rpcClient) RenewLocks(ctx context.Context, messages ...*Message) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.RenewLocks")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	lockTokens := make([]amqp.UUID, 0, len(messages))
	for _, m := range messages {
		if m.LockToken == nil {
			tab.For(ctx).Error(fmt.Errorf("failed: message has nil lock token, cannot renew lock"), tab.StringAttribute("messageId", m.ID))
			continue
		}

		amqpLockToken := amqp.UUID(*m.LockToken)
		lockTokens = append(lockTokens, amqpLockToken)
	}

	if len(lockTokens) < 1 {
		tab.For(ctx).Info("no lock tokens present to renew")
		return nil
	}

	renewRequestMsg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: lockRenewalOperationName,
		},
		Value: map[string]interface{}{
			lockTokensFieldName: lockTokens,
		},
	}

	rpcLink, err := rpc.NewLink(r.client, r.ec.ManagementPath())
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	response, err := rpcLink.RetryableRPC(ctx, 3, 1*time.Second, renewRequestMsg)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	if response.Code != 200 {
		err := fmt.Errorf("error renewing locks: %v", response.Description)
		tab.For(ctx).Error(err)
		return err
	}

	return nil
}

func (r *rpcClient) SendDisposition(ctx context.Context, m *Message, state disposition) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.rpcClient.SendDisposition")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	if m.LockToken == nil {
		err := errors.New("lock token on the message is not set, thus cannot send disposition")
		tab.For(ctx).Error(err)
		return err
	}

	var opts []rpc.LinkOption
	value := map[string]interface{}{
		"disposition-status": string(state.Status),
		"lock-tokens":        []amqp.UUID{amqp.UUID(*m.LockToken)},
	}

	if state.DeadLetterReason != nil {
		value["deadletter-reason"] = state.DeadLetterReason
	}

	if state.DeadLetterDescription != nil {
		value["deadletter-description"] = state.DeadLetterDescription
	}

	if m.useSession {
		value["session-id"] = m.sessionID
		opts = append(opts, rpc.LinkWithSessionFilter(m.sessionID))
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: "com.microsoft:update-disposition",
		},
		Value: value,
	}

	link, err := rpc.NewLink(r.client, m.ec.ManagementPath(), opts...)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	// no error, then it was successful
	_, err = link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	return nil
}

// ScheduleAt will send a batch of messages to a Queue, schedule them to be enqueued, and return the sequence numbers
// that can be used to cancel each message.
func (r *rpcClient) ScheduleAt(ctx context.Context, enqueueTime time.Time, messages ...*Message) ([]int64, error) {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.rpcClient.ScheduleAt")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	if len(messages) <= 0 {
		return nil, errors.New("expected one or more messages")
	}

	transformed := make([]interface{}, 0, len(messages))
	for i := range messages {
		messages[i].ScheduleAt(enqueueTime)

		if messages[i].ID == "" {
			id, err := uuid.NewV4()
			if err != nil {
				return nil, err
			}
			messages[i].ID = id.String()
		}

		rawAmqp, err := messages[i].toMsg()
		if err != nil {
			return nil, err
		}
		encoded, err := rawAmqp.MarshalBinary()
		if err != nil {
			return nil, err
		}

		individualMessage := map[string]interface{}{
			"message-id": messages[i].ID,
			"message":    encoded,
		}
		if messages[i].SessionID != nil {
			individualMessage["session-id"] = *messages[i].SessionID
		}
		if partitionKey := messages[i].SystemProperties.PartitionKey; partitionKey != nil {
			individualMessage["partition-key"] = *partitionKey
		}
		if viaPartitionKey := messages[i].SystemProperties.ViaPartitionKey; viaPartitionKey != nil {
			individualMessage["via-partition-key"] = *viaPartitionKey
		}

		transformed = append(transformed, individualMessage)
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: scheduleMessageOperationID,
		},
		Value: map[string]interface{}{
			"messages": transformed,
		},
	}

	if deadline, ok := ctx.Deadline(); ok {
		msg.ApplicationProperties[serverTimeoutFieldName] = uint(time.Until(deadline) / time.Millisecond)
	}

	link, err := rpc.NewLink(r.client, r.ec.ManagementPath())
	if err != nil {
		return nil, err
	}

	resp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		return nil, err
	}

	if resp.Code != 200 {
		return nil, ErrAMQP(*resp)
	}

	retval := make([]int64, 0, len(messages))
	if rawVal, ok := resp.Message.Value.(map[string]interface{}); ok {
		const sequenceFieldName = "sequence-numbers"
		if rawArr, ok := rawVal[sequenceFieldName]; ok {
			if arr, ok := rawArr.([]int64); ok {
				for i := range arr {
					retval = append(retval, arr[i])
				}
				return retval, nil
			}
			return nil, newErrIncorrectType(sequenceFieldName, []int64{}, rawArr)
		}
		return nil, ErrMissingField(sequenceFieldName)
	}
	return nil, newErrIncorrectType("value", map[string]interface{}{}, resp.Message.Value)
}

// CancelScheduled allows for removal of messages that have been handed to the Service Bus broker for later delivery,
// but have not yet ben enqueued.
func (r *rpcClient) CancelScheduled(ctx context.Context, seq ...int64) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.rpcClient.CancelScheduled")
	defer span.End()

	if err := r.ensureConn(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	r.clientMu.RLock()
	defer r.clientMu.RUnlock()

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			operationFieldName: cancelScheduledOperationID,
		},
		Value: map[string]interface{}{
			"sequence-numbers": seq,
		},
	}

	if deadline, ok := ctx.Deadline(); ok {
		msg.ApplicationProperties[serverTimeoutFieldName] = uint(time.Until(deadline) / time.Millisecond)
	}

	link, err := rpc.NewLink(r.client, r.ec.ManagementPath())
	if err != nil {
		return err
	}

	resp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		return err
	}

	if resp.Code != 200 {
		return ErrAMQP(*resp)
	}

	return nil
}

func (r *rpcClient) periodicallyRefreshAuth() {
	ctx, done := context.WithCancel(context.Background())
	r.doneRefreshingAuth = done

	ctx, span := r.startSpanFromContext(ctx, "sb.rpcClient.periodicallyRefreshAuth")
	defer span.End()

	doNegotiateClaimLocked := func(ctx context.Context, r *rpcClient) {
		r.clientMu.RLock()
		defer r.clientMu.RUnlock()

		if r.client != nil {
			if err := r.ec.Namespace().negotiateClaim(ctx, r.client, r.ec.ManagementPath()); err != nil {
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

func rpcClientWithSession(sessionID *string) rpcClientOption {
	return func(r *rpcClient) error {
		r.sessionID = sessionID
		r.isSessionFilterSet = true
		return nil
	}
}
