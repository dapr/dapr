package servicebus

import (
	"context"
	"sync"

	"github.com/devigned/tab"
)

type (
	// Closer provides the ability to close an entity
	Closer interface {
		Close(ctx context.Context) error
	}

	// ReceiveOner provides the ability to receive and handle events
	ReceiveOner interface {
		Closer
		ReceiveOne(ctx context.Context, handler Handler) error
	}

	// DeadLetterBuilder provides the ability to create a new receiver addressed to a given entity's dead letter queue.
	DeadLetterBuilder interface {
		NewDeadLetterReceiver(ctx context.Context, opts ...ReceiverOption) (ReceiveOner, error)
	}

	// TransferDeadLetterBuilder provides the ability to create a new receiver addressed to a given entity's transfer
	// dead letter queue.
	TransferDeadLetterBuilder interface {
		NewTransferDeadLetterReceiver(ctx context.Context, opts ...ReceiverOption) (ReceiveOner, error)
	}

	// DeadLetter represents a dead letter queue in Azure Service Bus.
	//
	// Azure Service Bus queues, topics and subscriptions provide a secondary sub-queue, called a dead-letter queue
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
	DeadLetter struct {
		builder  DeadLetterBuilder
		rMu      sync.Mutex
		receiver ReceiveOner
	}

	// TransferDeadLetter represents a transfer dead letter queue in Azure Service Bus.
	//
	// Messages will be sent to the transfer dead-letter queue under the following conditions:
	//   - A message passes through more than 3 queues or topics that are chained together.
	//   - The destination queue or topic is disabled or deleted.
	//   - The destination queue or topic exceeds the maximum entity size.
	TransferDeadLetter struct {
		builder  TransferDeadLetterBuilder
		rMu      sync.Mutex
		receiver ReceiveOner
	}
)

// NewDeadLetter constructs an instance of DeadLetter which represents a dead letter queue in Azure Service Bus
func NewDeadLetter(builder DeadLetterBuilder) *DeadLetter {
	return &DeadLetter{
		builder: builder,
	}
}

// ReceiveOne will receive one message from the dead letter queue
func (dl *DeadLetter) ReceiveOne(ctx context.Context, handler Handler) error {
	if err := dl.ensureReceiver(ctx); err != nil {
		return err
	}

	return dl.receiver.ReceiveOne(ctx, handler)
}

// Close the underlying connection to Service Bus
func (dl *DeadLetter) Close(ctx context.Context) error {
	dl.rMu.Lock()
	defer dl.rMu.Unlock()

	if dl.receiver == nil {
		return nil
	}

	if err := dl.receiver.Close(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	return nil
}

func (dl *DeadLetter) ensureReceiver(ctx context.Context) error {
	dl.rMu.Lock()
	defer dl.rMu.Unlock()

	r, err := dl.builder.NewDeadLetterReceiver(ctx)
	if err != nil {
		return err
	}

	dl.receiver = r
	return nil
}

// NewTransferDeadLetter constructs an instance of DeadLetter which represents a transfer dead letter queue in
// Azure Service Bus
func NewTransferDeadLetter(builder TransferDeadLetterBuilder) *TransferDeadLetter {
	return &TransferDeadLetter{
		builder: builder,
	}
}

// ReceiveOne will receive one message from the dead letter queue
func (dl *TransferDeadLetter) ReceiveOne(ctx context.Context, handler Handler) error {
	if err := dl.ensureReceiver(ctx); err != nil {
		return err
	}

	return dl.receiver.ReceiveOne(ctx, handler)
}

// Close the underlying connection to Service Bus
func (dl *TransferDeadLetter) Close(ctx context.Context) error {
	dl.rMu.Lock()
	defer dl.rMu.Unlock()

	if dl.receiver == nil {
		return nil
	}

	if err := dl.receiver.Close(ctx); err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	return nil
}

func (dl *TransferDeadLetter) ensureReceiver(ctx context.Context) error {
	dl.rMu.Lock()
	defer dl.rMu.Unlock()

	r, err := dl.builder.NewTransferDeadLetterReceiver(ctx)
	if err != nil {
		return err
	}

	dl.receiver = r
	return nil
}
