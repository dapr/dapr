package azqueue

import (
	"context"
	"github.com/Azure/azure-pipeline-go/pipeline"
	"net/http"
	"net/url"
	"time"
)

// A MessageIDURL represents a URL to a specific Azure Storage Queue message allowing you to manipulate the message.
type MessageIDURL struct {
	client messageIDClient
}

// NewMessageIDURL creates a MessageIDURL object using the specified URL and request policy pipeline.
func NewMessageIDURL(url url.URL, p pipeline.Pipeline) MessageIDURL {
	client := newMessageIDClient(url, p)
	return MessageIDURL{client: client}
}

// URL returns the URL endpoint used by the MessageIDURL object.
func (m MessageIDURL) URL() url.URL {
	return m.client.URL()
}

// String returns the URL as a string.
func (m MessageIDURL) String() string {
	u := m.URL()
	return u.String()
}

// WithPipeline creates a new MessageIDURL object identical to the source but with the specified request policy pipeline.
func (m MessageIDURL) WithPipeline(p pipeline.Pipeline) MessageIDURL {
	return NewMessageIDURL(m.URL(), p)
}

// Delete permanently removes the specified message from its queue.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/delete-message2.
func (m MessageIDURL) Delete(ctx context.Context, popReceipt PopReceipt) (*MessageIDDeleteResponse, error) {
	return m.client.Delete(ctx, string(popReceipt), nil, nil)
}

// Update changes a message's visibility timeout and contents. The message content must be a UTF-8 encoded string that is up to 64KB in size.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/update-message.
func (m MessageIDURL) Update(ctx context.Context, popReceipt PopReceipt, visibilityTimeout time.Duration, message string) (*UpdatedMessageResponse, error) {
	r, err := m.client.Update(ctx, QueueMessage{MessageText: message}, string(popReceipt),
		int32(visibilityTimeout.Seconds()), nil, nil)

	if err != nil {
		return nil, err
	}

	return &UpdatedMessageResponse{
		inner:           r,
		PopReceipt:      PopReceipt(r.PopReceipt()),
		TimeNextVisible: r.TimeNextVisible(),
	}, err
}

type UpdatedMessageResponse struct {
	inner *MessageIDUpdateResponse

	// PopReceipt returns the value for header x-ms-popreceipt.
	PopReceipt PopReceipt

	// TimeNextVisible returns the value for header x-ms-time-next-visible.
	TimeNextVisible time.Time
}

// Response returns the raw HTTP response object.
func (miur UpdatedMessageResponse) Response() *http.Response {
	return miur.inner.Response()
}

// StatusCode returns the HTTP status code of the response, e.g. 200.
func (miur UpdatedMessageResponse) StatusCode() int {
	return miur.inner.StatusCode()
}

// Status returns the HTTP status message of the response, e.g. "200 OK".
func (miur UpdatedMessageResponse) Status() string {
	return miur.inner.Status()
}

// Date returns the value for header Date.
func (miur UpdatedMessageResponse) Date() time.Time {
	return miur.inner.Date()
}

// RequestID returns the value for header x-ms-request-id.
func (miur UpdatedMessageResponse) RequestID() string {
	return miur.inner.RequestID()
}

// Version returns the value for header x-ms-version.
func (miur UpdatedMessageResponse) Version() string {
	return miur.inner.Version()
}
