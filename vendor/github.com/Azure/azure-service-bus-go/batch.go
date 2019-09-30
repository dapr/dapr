package servicebus

import (
	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"pack.ag/amqp"
)

type (
	// BatchOptions are optional information to add to a batch of messages
	BatchOptions struct {
		SessionID *string
	}

	// BatchIterator offers a simple mechanism for batching a list of messages
	BatchIterator interface {
		Done() bool
		Next(messageID string, opts *BatchOptions) (*MessageBatch, error)
	}

	// MessageBatchIterator provides an easy way to iterate over a slice of messages to reliably create batches
	MessageBatchIterator struct {
		Messages []*Message
		Cursor   int
		MaxSize  MaxMessageSizeInBytes
	}

	// MessageBatch represents a batch of messages to send to Service Bus in a single message
	MessageBatch struct {
		*Message
		marshaledMessages [][]byte
		MaxSize           MaxMessageSizeInBytes
		size              int
	}

	// MaxMessageSizeInBytes is the max number of bytes allowed by Azure Service Bus
	MaxMessageSizeInBytes int
)

const (
	// StandardMaxMessageSizeInBytes is the maximum number of bytes in a message for the Standard tier
	StandardMaxMessageSizeInBytes MaxMessageSizeInBytes = 256000
	// PremiumMaxMessageSizeInBytes is the maximum number of bytes in a message for the Premium tier
	PremiumMaxMessageSizeInBytes MaxMessageSizeInBytes = 1000000

	batchMessageFormat uint32 = 0x80013700

	batchMessageWrapperSize = 100
)

// NewMessageBatchIterator wraps a slice of Message pointers to allow it to be made into a MessageIterator.
func NewMessageBatchIterator(maxBatchSize MaxMessageSizeInBytes, msgs ...*Message) *MessageBatchIterator {
	return &MessageBatchIterator{
		Messages: msgs,
		MaxSize:  maxBatchSize,
	}
}

// Done communicates whether there are more messages remaining to be iterated over.
func (mbi *MessageBatchIterator) Done() bool {
	return len(mbi.Messages) == mbi.Cursor
}

// Next fetches the batch of messages in the message slice at a position one larger than the last one accessed.
func (mbi *MessageBatchIterator) Next(messageID string, opts *BatchOptions) (*MessageBatch, error) {
	if mbi.Done() {
		return nil, ErrNoMessages{}
	}

	if opts == nil {
		opts = &BatchOptions{}
	}

	mb := NewMessageBatch(mbi.MaxSize, messageID, opts)
	for mbi.Cursor < len(mbi.Messages) {
		ok, err := mb.Add(mbi.Messages[mbi.Cursor])
		if err != nil {
			return nil, err
		}

		if !ok {
			return mb, nil
		}
		mbi.Cursor++
	}
	return mb, nil
}

// NewMessageBatch builds a new message batch with a default standard max message size
func NewMessageBatch(maxSize MaxMessageSizeInBytes, messageID string, opts *BatchOptions) *MessageBatch {
	if opts == nil {
		opts = &BatchOptions{}
	}

	mb := &MessageBatch{
		MaxSize: maxSize,
		Message: &Message{
			ID:        messageID,
			SessionID: opts.SessionID,
		},
	}

	return mb
}

// Add adds a message to the batch if the message will not exceed the max size of the batch
func (mb *MessageBatch) Add(m *Message) (bool, error) {
	msg, err := m.toMsg()
	if err != nil {
		return false, err
	}

	if msg.Properties.MessageID == nil || msg.Properties.MessageID == "" {
		uid, err := uuid.NewV4()
		if err != nil {
			return false, err
		}
		msg.Properties.MessageID = uid.String()
	}

	if mb.SessionID != nil {
		msg.Properties.GroupID = *mb.SessionID
	}

	bin, err := msg.MarshalBinary()
	if err != nil {
		return false, err
	}

	if mb.Size()+len(bin) > int(mb.MaxSize) {
		return false, nil
	}

	mb.size += len(bin)
	mb.marshaledMessages = append(mb.marshaledMessages, bin)
	return true, nil
}

// Clear will zero out the batch size and clear the buffered messages
func (mb *MessageBatch) Clear() {
	mb.marshaledMessages = [][]byte{}
	mb.size = 0
}

// Size is the number of bytes in the message batch
func (mb *MessageBatch) Size() int {
	// calculated data size + batch message wrapper + data wrapper portions of the message
	return mb.size + batchMessageWrapperSize + (len(mb.marshaledMessages) * 5)
}

func (mb *MessageBatch) toMsg() (*amqp.Message, error) {
	batchMessage := mb.amqpBatchMessage()

	batchMessage.Data = make([][]byte, len(mb.marshaledMessages))
	for idx, bytes := range mb.marshaledMessages {
		batchMessage.Data[idx] = bytes
	}
	return batchMessage, nil
}

func (mb *MessageBatch) amqpBatchMessage() *amqp.Message {
	return &amqp.Message{
		Data:   make([][]byte, len(mb.marshaledMessages)),
		Format: batchMessageFormat,
		Properties: &amqp.MessageProperties{
			MessageID: mb.ID,
		},
	}
}
