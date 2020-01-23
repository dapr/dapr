package azqueue

import (
	"context"
	"net/url"
	"strings"

	"fmt"
	"github.com/Azure/azure-pipeline-go/pipeline"
)

const (
	// QueueMaxMessagesDequeue indicates the maximum number of messages
	// you can retrieve with each call to Dequeue (32).
	QueueMaxMessagesDequeue = 32

	// QueueMaxMessagesPeek indicates the maximum number of messages
	// you can retrieve with each call to Peek (32).
	QueueMaxMessagesPeek= 32

	// QueueMessageMaxBytes indicates the maximum number of bytes allowed for a message's UTF-8 text.
	QueueMessageMaxBytes = 64 * 1024 // 64KB
)

// A QueueURL represents a URL to the Azure Storage queue.
type QueueURL struct {
	client queueClient
}

// NewQueueURL creates a QueueURL object using the specified URL and request policy pipeline.
func NewQueueURL(url url.URL, p pipeline.Pipeline) QueueURL {
	client := newQueueClient(url, p)
	return QueueURL{client: client}
}

// URL returns the URL endpoint used by the QueueURL object.
func (q QueueURL) URL() url.URL {
	return q.client.URL()
}

// String returns the URL as a string.
func (q QueueURL) String() string {
	u := q.URL()
	return u.String()
}

// WithPipeline creates a new QueueURL object identical to the source but with the specified request policy pipeline.
func (q QueueURL) WithPipeline(p pipeline.Pipeline) QueueURL {
	return NewQueueURL(q.URL(), p)
}

// NewMessagesURL creates a new MessagesURL object by concatenating "messages" to the end of
// QueueURL's URL. The new MessagesURL uses the same request policy pipeline as the QueueURL.
// To change the pipeline, create the MessagesURL and then call its WithPipeline method passing in the
// desired pipeline object. Or, call this package's NewMessagesURL instead of calling this object's
// NewMessagesURL method.
func (q QueueURL) NewMessagesURL() MessagesURL {
	messagesURL := appendToURLPath(q.URL(), "messages")
	return NewMessagesURL(messagesURL, q.client.Pipeline())
}

// Create creates a queue within a storage account.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/create-queue4.
func (q QueueURL) Create(ctx context.Context, metadata Metadata) (*QueueCreateResponse, error) {
	return q.client.Create(ctx, nil, metadata, nil)
}

// Delete permanently deletes a queue.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/delete-queue3.
func (q QueueURL) Delete(ctx context.Context) (*QueueDeleteResponse, error) {
	return q.client.Delete(ctx, nil, nil)
}

// GetProperties retrieves queue properties and user-defined metadata and properties on the specified queue.
// Metadata is associated with the queue as name-values pairs.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/get-queue-metadata.
func (q QueueURL) GetProperties(ctx context.Context) (*QueueGetPropertiesResponse, error) {
	return q.client.GetProperties(ctx, nil, nil)
}

// SetMetadata sets user-defined metadata on the specified queue. Metadata is associated with the queue as name-value pairs.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/set-queue-metadata.
func (q QueueURL) SetMetadata(ctx context.Context, metadata Metadata) (*QueueSetMetadataResponse, error) {
	return q.client.SetMetadata(ctx, nil, metadata, nil)
}

// GetAccessPolicy returns details about any stored access policies specified on the queue that may be used with
// Shared Access Signatures.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/get-queue-acl.
func (q QueueURL) GetAccessPolicy(ctx context.Context) (*SignedIdentifiers, error) {
	return q.client.GetAccessPolicy(ctx, nil, nil)
}

// SetAccessPolicy sets sets stored access policies for the queue that may be used with Shared Access Signatures.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/set-queue-acl.
func (q QueueURL) SetAccessPolicy(ctx context.Context, permissions []SignedIdentifier) (*QueueSetAccessPolicyResponse, error) {
	return q.client.SetAccessPolicy(ctx, permissions, nil, nil)
}

// The AccessPolicyPermission type simplifies creating the permissions string for a queue's access policy.
// Initialize an instance of this type and then call its String method to set AccessPolicy's Permission field.
type AccessPolicyPermission struct {
	Read, Add, Update, ProcessMessages bool
}

// String produces the access policy permission string for an Azure Storage queue.
// Call this method to set AccessPolicy's Permission field.
func (p AccessPolicyPermission) String() string {
	var b strings.Builder
	if p.Read {
		b.WriteRune('r')
	}
	if p.Add {
		b.WriteRune('a')
	}
	if p.Update {
		b.WriteRune('u')
	}
	if p.ProcessMessages {
		b.WriteRune('p')
	}
	return b.String()
}

// Parse initializes the AccessPolicyPermission's fields from a string.
func (p *AccessPolicyPermission) Parse(s string) error {
	*p = AccessPolicyPermission{} // Clear the flags
	for _, r := range s {
		switch r {
		case 'r':
			p.Read = true
		case 'a':
			p.Add = true
		case 'u':
			p.Update = true
		case 'p':
			p.ProcessMessages = true
		default:
			return fmt.Errorf("invalid permission: '%v'", r)
		}
	}
	return nil
}
