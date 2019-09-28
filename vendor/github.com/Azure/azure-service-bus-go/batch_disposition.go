package servicebus

import (
	"context"
	"fmt"

	"github.com/Azure/azure-amqp-common-go/v2/uuid"
)

type (
	// MessageStatus defines an acceptable Message disposition status.
	MessageStatus dispositionStatus
	// BatchDispositionIterator provides an iterator over LockTokenIDs
	BatchDispositionIterator struct {
		LockTokenIDs []*uuid.UUID
		Status       MessageStatus
		cursor       int
	}
	// BatchDispositionError is an error which returns a collection of DispositionError.
	BatchDispositionError struct {
		Errors []DispositionError
	}
	// DispositionError is an error associated with a LockTokenID.
	DispositionError struct {
		LockTokenID *uuid.UUID
		err         error
	}
)

const (
	// Complete exposes completedDisposition
	Complete MessageStatus = MessageStatus(completedDisposition)
	// Abort exposes abandonedDisposition
	Abort MessageStatus = MessageStatus(abandonedDisposition)
)

func (bde BatchDispositionError) Error() string {
	msg := ""
	if len(bde.Errors) != 0 {
		msg = fmt.Sprintf("Operation failed, %d error(s) reported.", len(bde.Errors))
	}
	return msg
}

func (de DispositionError) Error() string {
	return de.err.Error()
}

// UnWrap will return the private error.
func (de DispositionError) UnWrap() error {
	return de.err
}

// Done communicates whether there are more messages remaining to be iterated over.
func (bdi *BatchDispositionIterator) Done() bool {
	return len(bdi.LockTokenIDs) == bdi.cursor
}

// Next iterates to the next LockToken
func (bdi *BatchDispositionIterator) Next() (uuid *uuid.UUID) {
	if done := bdi.Done(); done == false {
		uuid = bdi.LockTokenIDs[bdi.cursor]
		bdi.cursor++
	}
	return uuid
}

func (bdi *BatchDispositionIterator) doUpdate(ctx context.Context, ec entityConnector) BatchDispositionError {
	batchError := BatchDispositionError{}
	for !bdi.Done() {
		if id := bdi.Next(); id != nil {
			m := &Message{
				LockToken: id,
			}
			m.ec = ec
			err := m.sendDisposition(ctx, bdi.Status)
			if err != nil {
				batchError.Errors = append(batchError.Errors, DispositionError{
					LockTokenID: id,
					err:         err,
				})
			}
		}
	}
	return batchError
}

func (m *Message) sendDisposition(ctx context.Context, dispositionStatus MessageStatus) (err error) {
	switch dispositionStatus {
	case Complete:
		err = m.Complete(ctx)
	case Abort:
		err = m.Abandon(ctx)
	default:
		err = fmt.Errorf("unsupported bulk disposition status %q", dispositionStatus)
	}
	return err
}
