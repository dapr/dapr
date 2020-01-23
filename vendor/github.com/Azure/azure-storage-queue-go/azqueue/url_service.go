package azqueue

import (
	"context"
	"github.com/Azure/azure-pipeline-go/pipeline"
	"net/url"
)

// A ServiceURL represents a URL to the Azure Storage Queue service allowing you to manipulate queues.
type ServiceURL struct {
	client serviceClient
}

// NewServiceURL creates a ServiceURL object using the specified URL and request policy pipeline.
func NewServiceURL(primaryURL url.URL, p pipeline.Pipeline) ServiceURL {
	client := newServiceClient(primaryURL, p)
	return ServiceURL{client: client}
}

// URL returns the URL endpoint used by the ServiceURL object.
func (s ServiceURL) URL() url.URL {
	return s.client.URL()
}

// String returns the URL as a string.
func (s ServiceURL) String() string {
	u := s.URL()
	return u.String()
}

// WithPipeline creates a new ServiceURL object identical to the source but with the specified request policy pipeline.
func (s ServiceURL) WithPipeline(p pipeline.Pipeline) ServiceURL {
	return NewServiceURL(s.URL(), p)
}

// NewQueueURL creates a new QueueURL object by concatenating queueName to the end of
// ServiceURL's URL. The new QueueURL uses the same request policy pipeline as the ServiceURL.
// To change the pipeline, create the QueueURL and then call its WithPipeline method passing in the
// desired pipeline object. Or, call this package's NewQueueURL instead of calling this object's
// NewQueueURL method.
func (s ServiceURL) NewQueueURL(queueName string) QueueURL {
	queueURL := appendToURLPath(s.URL(), queueName)
	return NewQueueURL(queueURL, s.client.Pipeline())
}

// appendToURLPath appends a string to the end of a URL's path (prefixing the string with a '/' if required)
func appendToURLPath(u url.URL, name string) url.URL {
	// e.g. "https://ms.com/a/b/?k1=v1&k2=v2#f"
	// When you call url.Parse() this is what you'll get:
	//     Scheme: "https"
	//     Opaque: ""
	//       User: nil
	//       Host: "ms.com"
	//       Path: "/a/b/"	This should start with a / and it might or might not have a trailing slash
	//    RawPath: ""
	// ForceQuery: false
	//   RawQuery: "k1=v1&k2=v2"
	//   Fragment: "f"
	if len(u.Path) == 0 || u.Path[len(u.Path)-1] != '/' {
		u.Path += "/" // Append "/" to end before appending name
	}
	u.Path += name
	return u
}

// ListQueuesSegment returns a single segment of queues starting from the specified Marker. Use an empty
// Marker to start enumeration from the beginning. Queue names are returned in lexicographic order.
// After getting a segment, process it, and then call ListQueuesSegment again (passing the the previously-returned
// Marker) to get the next segment. For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/list-queues1.
func (s ServiceURL) ListQueuesSegment(ctx context.Context, marker Marker, o ListQueuesSegmentOptions) (*ListQueuesSegmentResponse, error) {
	prefix, include, maxResults := o.pointers()
	return s.client.ListQueuesSegment(ctx, prefix, marker.Val, maxResults,
		include, nil, nil)
}

// ListQueuesSegmentOptions defines options available when calling ListQueuesSegment.
type ListQueuesSegmentOptions struct {
	Detail ListQueuesSegmentDetails // No IncludeType header is produced if ""
	Prefix string                   // No Prefix header is produced if ""

	// SetMaxResults sets the maximum desired results you want the service to return.
	// Note, the service may return fewer results than requested.
	// MaxResults=0 means no 'MaxResults' header specified.
	MaxResults int32
}

func (o *ListQueuesSegmentOptions) pointers() (prefix *string, include ListQueuesIncludeType, maxResults *int32) {
	if o.Prefix != "" {
		prefix = &o.Prefix // else nil
	}
	if o.MaxResults != 0 {
		maxResults = &o.MaxResults
	}
	if o.Detail.Metadata {
		include = ListQueuesIncludeMetadata
	}
	return
}

// ListQueuesSegmentDetails indicates what additional information the service should return with each queue.
type ListQueuesSegmentDetails struct {
	// Tells the service whether to return metadata for each queue.
	Metadata bool
}

// slice produces the Include query parameter's value.
func (d *ListQueuesSegmentDetails) slice() []ListQueuesIncludeType {
	items := []ListQueuesIncludeType{}
	// NOTE: Multiple strings MUST be appended in alphabetic order or signing the string for authentication fails!
	if d.Metadata {
		items = append(items, ListQueuesIncludeMetadata)
	}
	return items
}

// GetProperties gets the properties of a storage account’s Queue service, including properties for Storage Analytics
// and CORS (Cross-Origin Resource Sharing) rules.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/get-queue-service-properties.
func (s ServiceURL) GetProperties(ctx context.Context) (*StorageServiceProperties, error) {
	return s.client.GetProperties(ctx, nil, nil)
}

// SetProperties sets properties for a storage account’s Queue service endpoint, including properties for Storage Analytics
// and CORS (Cross-Origin Resource Sharing) rules.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/set-queue-service-properties.
func (s ServiceURL) SetProperties(ctx context.Context, properties StorageServiceProperties) (*ServiceSetPropertiesResponse, error) {
	return s.client.SetProperties(ctx, properties, nil, nil)
}

// GetStatistics retrieves statistics related to replication for the Queue service. It is only available on the
// secondary location endpoint when read-access geo-redundant replication is enabled for the storage account.
// For more information, see https://docs.microsoft.com/en-us/rest/api/storageservices/get-queue-service-stats.
func (s ServiceURL) GetStatistics(ctx context.Context) (*StorageServiceStats, error) {
	return s.client.GetStatistics(ctx, nil, nil)
}
