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
	"encoding/xml"
	"fmt"
	"strings"
	"sync"

	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"github.com/Azure/go-autorest/autorest/date"
	"github.com/devigned/tab"
)

type (

	// Queue represents a Service Bus Queue entity, which offers First In, First Out (FIFO) message delivery to one or
	// more competing consumers. That is, messages are typically expected to be received and processed by the receivers
	// in the order in which they were added to the queue, and each message is received and processed by only one
	// message consumer.
	Queue struct {
		*sendAndReceiveEntity
		sender        *Sender
		receiver      *Receiver
		receiverMu    sync.Mutex
		senderMu      sync.Mutex
		receiveMode   ReceiveMode
		prefetchCount *uint32
	}

	// queueContent is a specialized Queue body for an Atom entry
	queueContent struct {
		XMLName          xml.Name         `xml:"content"`
		Type             string           `xml:"type,attr"`
		QueueDescription QueueDescription `xml:"QueueDescription"`
	}

	// QueueDescription is the content type for Queue management requests
	QueueDescription struct {
		XMLName xml.Name `xml:"QueueDescription"`
		BaseEntityDescription
		LockDuration                        *string       `xml:"LockDuration,omitempty"`               // LockDuration - ISO 8601 timespan duration of a peek-lock; that is, the amount of time that the message is locked for other receivers. The maximum value for LockDuration is 5 minutes; the default value is 1 minute.
		MaxSizeInMegabytes                  *int32        `xml:"MaxSizeInMegabytes,omitempty"`         // MaxSizeInMegabytes - The maximum size of the queue in megabytes, which is the size of memory allocated for the queue. Default is 1024.
		RequiresDuplicateDetection          *bool         `xml:"RequiresDuplicateDetection,omitempty"` // RequiresDuplicateDetection - A value indicating if this queue requires duplicate detection.
		RequiresSession                     *bool         `xml:"RequiresSession,omitempty"`
		DefaultMessageTimeToLive            *string       `xml:"DefaultMessageTimeToLive,omitempty"`            // DefaultMessageTimeToLive - ISO 8601 default message timespan to live value. This is the duration after which the message expires, starting from when the message is sent to Service Bus. This is the default value used when TimeToLive is not set on a message itself.
		DeadLetteringOnMessageExpiration    *bool         `xml:"DeadLetteringOnMessageExpiration,omitempty"`    // DeadLetteringOnMessageExpiration - A value that indicates whether this queue has dead letter support when a message expires.
		DuplicateDetectionHistoryTimeWindow *string       `xml:"DuplicateDetectionHistoryTimeWindow,omitempty"` // DuplicateDetectionHistoryTimeWindow - ISO 8601 timeSpan structure that defines the duration of the duplicate detection history. The default value is 10 minutes.
		MaxDeliveryCount                    *int32        `xml:"MaxDeliveryCount,omitempty"`                    // MaxDeliveryCount - The maximum delivery count. A message is automatically deadlettered after this number of deliveries. default value is 10.
		EnableBatchedOperations             *bool         `xml:"EnableBatchedOperations,omitempty"`             // EnableBatchedOperations - Value that indicates whether server-side batched operations are enabled.
		SizeInBytes                         *int64        `xml:"SizeInBytes,omitempty"`                         // SizeInBytes - The size of the queue, in bytes.
		MessageCount                        *int64        `xml:"MessageCount,omitempty"`                        // MessageCount - The number of messages in the queue.
		IsAnonymousAccessible               *bool         `xml:"IsAnonymousAccessible,omitempty"`
		Status                              *EntityStatus `xml:"Status,omitempty"`
		CreatedAt                           *date.Time    `xml:"CreatedAt,omitempty"`
		UpdatedAt                           *date.Time    `xml:"UpdatedAt,omitempty"`
		SupportOrdering                     *bool         `xml:"SupportOrdering,omitempty"`
		AutoDeleteOnIdle                    *string       `xml:"AutoDeleteOnIdle,omitempty"`
		EnablePartitioning                  *bool         `xml:"EnablePartitioning,omitempty"`
		EnableExpress                       *bool         `xml:"EnableExpress,omitempty"`
		CountDetails                        *CountDetails `xml:"CountDetails,omitempty"`
		ForwardTo                           *string       `xml:"ForwardTo,omitempty"`
		ForwardDeadLetteredMessagesTo       *string       `xml:"ForwardDeadLetteredMessagesTo,omitempty"` // ForwardDeadLetteredMessagesTo - absolute URI of the entity to forward dead letter messages
	}

	// QueueOption represents named options for assisting Queue message handling
	QueueOption func(*Queue) error

	// ReceiveMode represents the behavior when consuming a message from a queue
	ReceiveMode int

	entityConnector interface {
		EntityManagementAddresser
		Namespace() *Namespace
		getEntity() *entity
	}
)

const (
	// PeekLockMode causes a Receiver to peek at a message, lock it so no others can consume and have the queue wait for
	// the DispositionAction
	PeekLockMode ReceiveMode = 0
	// ReceiveAndDeleteMode causes a Receiver to pop messages off of the queue without waiting for DispositionAction
	ReceiveAndDeleteMode ReceiveMode = 1

	// DeadLetterQueueName is the name of the dead letter queue to be appended to the entity path
	DeadLetterQueueName = "$DeadLetterQueue"

	// TransferDeadLetterQueueName is the name of the transfer dead letter queue which is appended to the entity name to
	// build the full address of the transfer dead letter queue.
	TransferDeadLetterQueueName = "$Transfer/" + DeadLetterQueueName
)

// QueueWithReceiveAndDelete configures a queue to pop and delete messages off of the queue upon receiving the message.
// This differs from the default, PeekLock, where PeekLock receives a message, locks it for a period of time, then sends
// a disposition to the broker when the message has been processed.
func QueueWithReceiveAndDelete() QueueOption {
	return func(q *Queue) error {
		q.receiveMode = ReceiveAndDeleteMode
		return nil
	}
}

// QueueWithPrefetchCount configures the queue to attempt to fetch the number of messages specified by the
// prefetch count at one time.
//
// The default is 1 message at a time.
//
// Caution: Using PeekLock, messages have a set lock timeout, which can be renewed. By setting a high prefetch count, a
// local queue of messages could build up and cause message locks to expire before the message lands in the handler. If
// this happens, the message disposition will fail and will be re-queued and processed again.
func QueueWithPrefetchCount(prefetch uint32) QueueOption {
	return func(q *Queue) error {
		q.prefetchCount = &prefetch
		return nil
	}
}

// NewQueue creates a new Queue Sender / Receiver
func (ns *Namespace) NewQueue(name string, opts ...QueueOption) (*Queue, error) {
	entity := newEntity(name, queueManagementPath(name), ns)
	queue := &Queue{
		sendAndReceiveEntity: newSendAndReceiveEntity(entity),
		receiveMode:          PeekLockMode,
	}

	for _, opt := range opts {
		if err := opt(queue); err != nil {
			return nil, err
		}
	}
	return queue, nil
}

// Send sends messages to the Queue
func (q *Queue) Send(ctx context.Context, msg *Message) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.Send")
	defer span.End()

	err := q.ensureSender(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}
	return q.sender.Send(ctx, msg)
}

// SendBatch sends a batch of messages to the Queue
func (q *Queue) SendBatch(ctx context.Context, iterator BatchIterator) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.SendBatch")
	defer span.End()

	err := q.ensureSender(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	for !iterator.Done() {
		id, err := uuid.NewV4()
		if err != nil {
			tab.For(ctx).Error(err)
			return err
		}

		batch, err := iterator.Next(id.String(), &BatchOptions{
			SessionID: q.sender.sessionID,
		})
		if err != nil {
			tab.For(ctx).Error(err)
			return err
		}

		if err := q.sender.trySend(ctx, batch); err != nil {
			tab.For(ctx).Error(err)
			return err
		}
	}

	return nil
}

// ReceiveOne will listen to receive a single message. ReceiveOne will only wait as long as the context allows.
//
// Handler must call a disposition action such as Complete, Abandon, Deadletter on the message. If the messages does not
// have a disposition set, the Queue's DefaultDisposition will be used.
func (q *Queue) ReceiveOne(ctx context.Context, handler Handler) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.ReceiveOne")
	defer span.End()

	if err := q.ensureReceiver(ctx); err != nil {
		return err
	}

	return q.receiver.ReceiveOne(ctx, handler)
}

// Receive subscribes for messages sent to the Queue. If the messages not within a session, messages will arrive
// unordered.
//
// Handler must call a disposition action such as Complete, Abandon, Deadletter on the message. If the messages does not
// have a disposition set, the Queue's DefaultDisposition will be used.
//
// If the handler returns an error, the receive loop will be terminated.
func (q *Queue) Receive(ctx context.Context, handler Handler) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.Receive")
	defer span.End()

	err := q.ensureReceiver(ctx)
	if err != nil {
		return err
	}

	handle := q.receiver.Listen(ctx, handler)
	<-handle.Done()
	return handle.Err()
}

// NewSession will create a new session based receiver and sender for the queue
//
// Microsoft Azure Service Bus sessions enable joint and ordered handling of unbounded sequences of related messages.
// To realize a FIFO guarantee in Service Bus, use Sessions. Service Bus is not prescriptive about the nature of the
// relationship between the messages, and also does not define a particular model for determining where a message
// sequence starts or ends.
func (q *Queue) NewSession(sessionID *string) *QueueSession {
	return NewQueueSession(q, sessionID)
}

// NewReceiver will create a new Receiver for receiving messages off of a queue
func (q *Queue) NewReceiver(ctx context.Context, opts ...ReceiverOption) (*Receiver, error) {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.NewReceiver")
	defer span.End()

	opts = append(opts, ReceiverWithReceiveMode(q.receiveMode))
	return q.namespace.NewReceiver(ctx, q.Name, opts...)
}

// NewSender will create a new Sender for sending messages to the queue
func (q *Queue) NewSender(ctx context.Context, opts ...SenderOption) (*Sender, error) {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.NewSender")
	defer span.End()

	return q.namespace.NewSender(ctx, q.Name)
}

// NewDeadLetter creates an entity that represents the dead letter sub queue of the queue
//
// Azure Service Bus queues and topic subscriptions provide a secondary sub-queue, called a dead-letter queue
// (DLQ). The dead-letter queue does not need to be explicitly created and cannot be deleted or otherwise managed
// independent of the main entity.
//
// The purpose of the dead-letter queue is to hold messages that cannot be delivered to any receiver, or messages
// that could not be processed. Messages can then be removed from the DLQ and inspected. An application might, with
// help of an operator, correct issues and resubmit the message, log the fact that there was an error, and take
// corrective action.
//
// From an API and protocol perspective, the DLQ is mostly similar to any other queue, except that messages can only
// be submitted via the dead-letter operation of the parent entity. In addition, time-to-live is not observed, and
// you can't dead-letter a message from a DLQ. The dead-letter queue fully supports peek-lock delivery and
// transactional operations.
//
// Note that there is no automatic cleanup of the DLQ. Messages remain in the DLQ until you explicitly retrieve
// them from the DLQ and call Complete() on the dead-letter message.
func (q *Queue) NewDeadLetter() *DeadLetter {
	return NewDeadLetter(q)
}

// NewDeadLetterReceiver builds a receiver for the Queue's dead letter queue
func (q *Queue) NewDeadLetterReceiver(ctx context.Context, opts ...ReceiverOption) (ReceiveOner, error) {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.NewDeadLetterReceiver")
	defer span.End()

	deadLetterEntityPath := strings.Join([]string{q.Name, DeadLetterQueueName}, "/")
	return q.namespace.NewReceiver(ctx, deadLetterEntityPath, opts...)
}

// NewTransferDeadLetter creates an entity that represents the transfer dead letter sub queue of the queue
//
// Messages will be sent to the transfer dead-letter queue under the following conditions:
//   - A message passes through more than 3 queues or topics that are chained together.
//   - The destination queue or topic is disabled or deleted.
//   - The destination queue or topic exceeds the maximum entity size.
func (q *Queue) NewTransferDeadLetter() *TransferDeadLetter {
	return NewTransferDeadLetter(q)
}

// NewTransferDeadLetterReceiver builds a receiver for the Queue's transfer dead letter queue
//
// Messages will be sent to the transfer dead-letter queue under the following conditions:
//   - A message passes through more than 3 queues or topics that are chained together.
//   - The destination queue or topic is disabled or deleted.
//   - The destination queue or topic exceeds the maximum entity size.
func (q *Queue) NewTransferDeadLetterReceiver(ctx context.Context, opts ...ReceiverOption) (ReceiveOner, error) {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.NewTransferDeadLetterReceiver")
	defer span.End()

	transferDeadLetterEntityPath := strings.Join([]string{q.Name, TransferDeadLetterQueueName}, "/")
	return q.namespace.NewReceiver(ctx, transferDeadLetterEntityPath, opts...)
}

// Close the underlying connection to Service Bus
func (q *Queue) Close(ctx context.Context) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.Close")
	defer span.End()

	var lastErr error
	if q.receiver != nil {
		if err := q.receiver.Close(ctx); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
		q.receiver = nil
	}

	if q.sender != nil {
		if err := q.sender.Close(ctx); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
		q.sender = nil
	}

	if q.rpcClient != nil {
		if err := q.rpcClient.Close(); err != nil && !isConnectionClosed(err) {
			tab.For(ctx).Error(err)
			lastErr = err
		}
		q.rpcClient = nil
	}

	return lastErr
}

func isConnectionClosed(err error) bool {
	return err.Error() == "amqp: connection closed"
}

func (q *Queue) newReceiver(ctx context.Context, opts ...ReceiverOption) (*Receiver, error) {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.NewReceiver")
	defer span.End()

	opts = append(opts, ReceiverWithReceiveMode(q.receiveMode))

	if q.prefetchCount != nil {
		opts = append(opts, ReceiverWithPrefetchCount(*q.prefetchCount))
	}

	return q.namespace.NewReceiver(ctx, q.Name, opts...)
}

func (q *Queue) ensureReceiver(ctx context.Context, opts ...ReceiverOption) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.ensureReceiver")
	defer span.End()

	q.receiverMu.Lock()
	defer q.receiverMu.Unlock()

	if q.receiver != nil {
		return nil
	}

	receiver, err := q.newReceiver(ctx, opts...)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	q.receiver = receiver
	return nil
}

func (q *Queue) ensureSender(ctx context.Context) error {
	ctx, span := q.startSpanFromContext(ctx, "sb.Queue.ensureSender")
	defer span.End()

	q.senderMu.Lock()
	defer q.senderMu.Unlock()

	if q.sender != nil {
		return nil
	}

	s, err := q.NewSender(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}
	q.sender = s
	return nil
}

func queueManagementPath(qName string) string {
	return fmt.Sprintf("%s/$management", qName)
}
