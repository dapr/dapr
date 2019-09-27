package servicebus

import (
	"context"
	"errors"

	"github.com/devigned/tab"
)

type (
	// MessageIterator offers a simple mechanism for iterating over a list of
	MessageIterator interface {
		Done() bool
		Next(context.Context) (*Message, error)
	}

	// MessageSliceIterator is a wrapper, which lets any slice of Message pointers be used as a MessageIterator.
	MessageSliceIterator struct {
		Target []*Message
		Cursor int
	}

	peekIterator struct {
		entity             *entity
		buffer             chan *Message
		lastSequenceNumber int64
	}

	// PeekOption allows customization of parameters when querying a Service Bus entity for messages without committing
	// to processing them.
	PeekOption func(*peekIterator) error
)

const (
	defaultPeekPageSize = 10
)

// AsMessageSliceIterator wraps a slice of Message pointers to allow it to be made into a MessageIterator.
func AsMessageSliceIterator(target []*Message) *MessageSliceIterator {
	return &MessageSliceIterator{
		Target: target,
	}
}

// Done communicates whether there are more messages remaining to be iterated over.
func (ms MessageSliceIterator) Done() bool {
	return ms.Cursor >= len(ms.Target)
}

// Next fetches the Message in the slice at a position one larger than the last one accessed.
func (ms *MessageSliceIterator) Next(_ context.Context) (*Message, error) {
	if ms.Done() {
		return nil, ErrNoMessages{}
	}

	retval := ms.Target[ms.Cursor]
	ms.Cursor++
	return retval, nil
}

func newPeekIterator(entity *entity, options ...PeekOption) (*peekIterator, error) {
	retval := &peekIterator{
		entity: entity,
	}

	foundPageSize := false
	for i := range options {
		if err := options[i](retval); err != nil {
			return nil, err
		}

		if retval.buffer != nil {
			foundPageSize = true
		}
	}

	if !foundPageSize {
		err := PeekWithPageSize(defaultPeekPageSize)(retval)
		if err != nil {
			return nil, err
		}
	}

	return retval, nil
}

// PeekWithPageSize adjusts how many messages are fetched at once while peeking from the server.
func PeekWithPageSize(pageSize int) PeekOption {
	return func(pi *peekIterator) error {
		if pageSize < 0 {
			return errors.New("page size must not be less than zero")
		}

		if pi.buffer != nil {
			return errors.New("cannot modify an existing peekIterator's buffer")
		}

		pi.buffer = make(chan *Message, pageSize)
		return nil
	}
}

// PeekFromSequenceNumber adds a filter to the Peek operation, so that no messages with a Sequence Number less than
// 'seq' are returned.
func PeekFromSequenceNumber(seq int64) PeekOption {
	return func(pi *peekIterator) error {
		pi.lastSequenceNumber = seq + 1
		return nil
	}
}

func (pi peekIterator) Done() bool {
	return false
}

func (pi *peekIterator) Next(ctx context.Context) (*Message, error) {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.peekIterator.Next")
	defer span.End()

	if len(pi.buffer) == 0 {
		if err := pi.getNextPage(ctx); err != nil {
			return nil, err
		}
	}

	select {
	case next := <-pi.buffer:
		return next, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (pi *peekIterator) getNextPage(ctx context.Context) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.peekIterator.getNextPage")
	defer span.End()

	client, err := pi.entity.GetRPCClient(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	msgs, err := client.GetNextPage(ctx, pi.lastSequenceNumber, int32(cap(pi.buffer)))
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	for i := range msgs {
		select {
		case pi.buffer <- msgs[i]:
			// Intentionally Left Blank
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Update last seen sequence number so that the next read starts from where this ended.
	lastMsg := msgs[len(msgs)-1]
	pi.lastSequenceNumber = *lastMsg.SystemProperties.SequenceNumber + 1
	return nil
}
