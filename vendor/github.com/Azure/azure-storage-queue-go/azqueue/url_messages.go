package azqueue

import (
	"context"
	"net/url"
	"time"

	"github.com/Azure/azure-pipeline-go/pipeline"
	"net/http"
)

// MessageID represents a Message ID as a string.
type MessageID string

// String returns a MessageID as a string
func (id MessageID) String() string { return string(id) }

///////////////////////////////////////////////////////////////////////////////

// PopReceipt represents a Message's opaque pop receipt.
type PopReceipt string

// String returns a PopReceipt as a string
func (pr PopReceipt) String() string { return string(pr) }

///////////////////////////////////////////////////////////////////////////////

// A MessagesURL represents a URL to an Azure Storage Queue's messages allowing you to manipulate its messages.
type MessagesURL struct {
	client messagesClient
}

// NewMessageURL creates a MessagesURL object using the specified URL and request policy pipeline.
func NewMessagesURL(url url.URL, p pipeline.Pipeline) MessagesURL {
	client := newMessagesClient(url, p)
	return MessagesURL{client: client}
}

// URL returns the URL endpoint used by the MessagesURL object.
func (m MessagesURL) URL() url.URL {
	return m.client.URL()
}

// String returns the URL as a string.
func (m MessagesURL) String() string {
	u := m.URL()
	return u.String()
}

// WithPipeline creates a new MessagesURL object identical to the source but with the specified request policy pipeline.
func (m MessagesURL) WithPipeline(p pipeline.Pipeline) MessagesURL {
	return NewMessagesURL(m.URL(), p)
}

// NewMessageIDURL creates a new MessageIDURL object by concatenating messageID to the end of
// MessagesURL's URL. The new MessageIDURL uses the same request policy pipeline as the MessagesURL.
// To change the pipeline, create the MessageIDURL and then call its WithPipeline method passing in the
// desired pipeline object. Or, call this package's NewMessageIDURL instead of calling this object's
// NewMessageIDURL method.
func (m MessagesURL) NewMessageIDURL(messageID MessageID) MessageIDURL {
	messageIDURL := appendToURLPath(m.URL(), messageID.String())
	return NewMessageIDURL(messageIDURL, m.client.Pipeline())
}

// Clear deletes all messages from a queue. For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/clear-messages.
func (m MessagesURL) Clear(ctx context.Context) (*MessagesClearResponse, error) {
	return m.client.Clear(ctx, nil, nil)
}

///////////////////////////////////////////////////////////////////////////////

// Enqueue adds a new message to the back of a queue. The visibility timeout specifies how long the message should be invisible
// to Dequeue and Peek operations. The message content must be a UTF-8 encoded string that is up to 64KB in size.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/put-message.
// The timeToLive interval for the message is defined in seconds. The maximum timeToLive can be any positive number, as well as -time.Second indicating that the message does not expire.
// If 0 is passed for timeToLive, the default value is 7 days.
func (m MessagesURL) Enqueue(ctx context.Context, messageText string, visibilityTimeout time.Duration, timeToLive time.Duration) (*EnqueueMessageResponse, error) {
	vt := int32(visibilityTimeout.Seconds())

	// timeToLive should only be sent if it's not 0
	var ttl *int32 = nil
	if timeToLive != 0 {
		ttlValue := int32(timeToLive.Seconds())
		ttl = &ttlValue
	}

	resp, err := m.client.Enqueue(ctx, QueueMessage{MessageText: messageText}, &vt, ttl, nil, nil)
	if err != nil {
		return nil, err
	}

	item := resp.Items[0]
	return &EnqueueMessageResponse{
		inner:           resp,
		MessageID:       MessageID(item.MessageID),
		PopReceipt:      PopReceipt(item.PopReceipt),
		TimeNextVisible: item.TimeNextVisible,
		InsertionTime:   item.InsertionTime,
		ExpirationTime:  item.ExpirationTime,
	}, nil
}

// EnqueueMessageResponse holds the results of a successfully-enqueued message.
type EnqueueMessageResponse struct {
	inner      *EnqueueResponse

	// MessageID returns the service-assigned ID for the enqueued message.
	MessageID  MessageID

	// PopReceipt returns the service-assigned PopReceipt for the enqueued message.
	// You could use this to create a MessageIDURL object.
	PopReceipt PopReceipt

	// TimeNextVisible returns the time when the message next becomes visible.
	TimeNextVisible time.Time

	// InsertionTime returns the time when the message was enqueued.
	InsertionTime time.Time

	// ExpirationTime returns the time when the message will automatically be deleted from the queue.
	ExpirationTime time.Time
}

// Response returns the raw HTTP response object.
func (emr EnqueueMessageResponse) Response() *http.Response {
	return emr.inner.Response()
}

// StatusCode returns the HTTP status code of the response, e.g. 200.
func (emr EnqueueMessageResponse) StatusCode() int {
	return emr.inner.StatusCode()
}

// Status returns the HTTP status message of the response, e.g. "200 OK".
func (emr EnqueueMessageResponse) Status() string {
	return emr.inner.Status()
}

// Date returns the value for header Date.
func (emr EnqueueMessageResponse) Date() time.Time {
	return emr.inner.Date()
}

// RequestID returns the value for header x-ms-request-id.
func (emr EnqueueMessageResponse) RequestID() string {
	return emr.inner.RequestID()
}

// Version returns the value for header x-ms-version.
func (emr EnqueueMessageResponse) Version() string {
	return emr.inner.Version()
}

///////////////////////////////////////////////////////////////////////////////

// Dequeue retrieves one or more messages from the front of the queue.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/get-messages.
func (m MessagesURL) Dequeue(ctx context.Context, maxMessages int32, visibilityTimeout time.Duration) (*DequeuedMessagesResponse, error) {
	vt := int32(visibilityTimeout.Seconds())
	qml, err := m.client.Dequeue(ctx, &maxMessages, &vt, nil, nil)
	return &DequeuedMessagesResponse{inner: qml}, err
}

// DequeueMessagesResponse holds the results of a successful call to Dequeue.
type DequeuedMessagesResponse struct {
	inner *QueueMessagesList
}

// Response returns the raw HTTP response object.
func (dmr DequeuedMessagesResponse) Response() *http.Response {
	return dmr.inner.Response()
}

// StatusCode returns the HTTP status code of the response, e.g. 200.
func (dmr DequeuedMessagesResponse) StatusCode() int {
	return dmr.inner.StatusCode()
}

// Status returns the HTTP status message of the response, e.g. "200 OK".
func (dmr DequeuedMessagesResponse) Status() string {
	return dmr.inner.Status()
}

// Date returns the value for header Date.
func (dmr DequeuedMessagesResponse) Date() time.Time {
	return dmr.inner.Date()
}

// RequestID returns the value for header x-ms-request-id.
func (dmr DequeuedMessagesResponse) RequestID() string {
	return dmr.inner.RequestID()
}

// Version returns the value for header x-ms-version.
func (dmr DequeuedMessagesResponse) Version() string {
	return dmr.inner.Version()
}

// NumMessages returns the number of messages retrieved by the call to Dequeue.
func (dmr DequeuedMessagesResponse) NumMessages() int32 {
	return int32(len(dmr.inner.Items))
}

// Message returns the information for dequeued message.
func (dmr DequeuedMessagesResponse) Message(index int32) *DequeuedMessage {
	v := dmr.inner.Items[index]
	return &DequeuedMessage{
		ID:              MessageID(v.MessageID),
		InsertionTime:   v.InsertionTime,
		ExpirationTime:  v.ExpirationTime,
		PopReceipt:      PopReceipt(v.PopReceipt),
		NextVisibleTime: v.TimeNextVisible,
		Text:            v.MessageText,
		DequeueCount:    v.DequeueCount,
	}
}

// DequeuedMessage holds the properties of a single dequeued message.
type DequeuedMessage struct {
	ID              MessageID
	InsertionTime   time.Time
	ExpirationTime  time.Time
	PopReceipt      PopReceipt
	NextVisibleTime time.Time
	DequeueCount    int64
	Text            string // UTF-8 string
}

///////////////////////////////////////////////////////////////////////////////

// Peek retrieves one or more messages from the front of the queue but does not alter the visibility of the message.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/peek-messages.
func (m MessagesURL) Peek(ctx context.Context, maxMessages int32) (*PeekedMessagesResponse, error) {
	pr, err := m.client.Peek(ctx, &maxMessages, nil, nil)
	return &PeekedMessagesResponse{inner: pr}, err
}

// PeekedMessagesResponse holds the results of a successful call to Peek.
type PeekedMessagesResponse struct {
	inner *PeekResponse
}

// Response returns the raw HTTP response object.
func (pmr PeekedMessagesResponse) Response() *http.Response {
	return pmr.inner.Response()
}

// StatusCode returns the HTTP status code of the response, e.g. 200.
func (pmr PeekedMessagesResponse) StatusCode() int {
	return pmr.inner.StatusCode()
}

// Status returns the HTTP status message of the response, e.g. "200 OK".
func (pmr PeekedMessagesResponse) Status() string {
	return pmr.inner.Status()
}

// Date returns the value for header Date.
func (pmr PeekedMessagesResponse) Date() time.Time {
	return pmr.inner.Date()
}

// RequestID returns the value for header x-ms-request-id.
func (pmr PeekedMessagesResponse) RequestID() string {
	return pmr.inner.RequestID()
}

// Version returns the value for header x-ms-version.
func (pmr PeekedMessagesResponse) Version() string {
	return pmr.inner.Version()
}

// NumMessages returns the number of messages retrieved by the call to Peek.
func (pmr PeekedMessagesResponse) NumMessages() int32 {
	return int32(len(pmr.inner.Items))
}

// Message returns the information for peeked message.
func (pmr PeekedMessagesResponse) Message(index int32) *PeekedMessage {
	v := pmr.inner.Items[index]
	return &PeekedMessage{
		ID:             MessageID(v.MessageID),
		InsertionTime:  v.InsertionTime,
		ExpirationTime: v.ExpirationTime,
		Text:           v.MessageText,
		DequeueCount:   v.DequeueCount,
	}
}

// PeekedMessage holds the properties of a peeked message.
type PeekedMessage struct {
	ID             MessageID
	InsertionTime  time.Time
	ExpirationTime time.Time
	DequeueCount   int64
	Text           string // UTF-8 string
}
