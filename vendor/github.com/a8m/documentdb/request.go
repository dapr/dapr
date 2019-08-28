package documentdb

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderXDate               = "X-Ms-Date"
	HeaderAuth                = "Authorization"
	HeaderVersion             = "X-Ms-Version"
	HeaderContentType         = "Content-Type"
	HeaderContentLength       = "Content-Length"
	HeaderIsQuery             = "X-Ms-Documentdb-Isquery"
	HeaderUpsert              = "x-ms-documentdb-is-upsert"
	HeaderPartitionKey        = "x-ms-documentdb-partitionkey"
	HeaderMaxItemCount        = "x-ms-max-item-count"
	HeaderContinuation        = "x-ms-continuation"
	HeaderConsistency         = "x-ms-consistency-level"
	HeaderSessionToken        = "x-ms-session-token"
	HeaderCrossPartition      = "x-ms-documentdb-query-enablecrosspartition"
	HeaderIfMatch             = "If-Match"
	HeaderIfNonMatch          = "If-None-Match"
	HeaderIfModifiedSince     = "If-Modified-Since"
	HeaderActivityID          = "x-ms-activity-id"
	HeaderRequestCharge       = "x-ms-request-charge"
	HeaderAIM                 = "A-IM"
	HeaderPartitionKeyRangeID = "x-ms-documentdb-partitionkeyrangeid"

	SupportedVersion = "2017-02-22"
)

// Request Error
type RequestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Implement Error function
func (e RequestError) Error() string {
	return fmt.Sprintf("%v, %v", e.Code, e.Message)
}

// Resource Request
type Request struct {
	rId, rType string
	*http.Request
}

// Return new resource request with type and id
func ResourceRequest(link string, req *http.Request) *Request {
	rId, rType := parse(link)
	return &Request{rId, rType, req}
}

// Add 3 default headers to *Request
// "x-ms-date", "x-ms-version", "authorization"
func (req *Request) DefaultHeaders(mKey *Key) (err error) {
	req.Header.Add(HeaderXDate, formatDate(time.Now()))
	req.Header.Add(HeaderVersion, SupportedVersion)

	b := buffers.Get().(*bytes.Buffer)
	b.Reset()
	b.WriteString(req.Method)
	b.WriteRune('\n')
	b.WriteString(req.rType)
	b.WriteRune('\n')
	b.WriteString(req.rId)
	b.WriteRune('\n')
	b.WriteString(req.Header.Get(HeaderXDate))
	b.WriteRune('\n')
	b.WriteString(req.Header.Get("Date"))
	b.WriteRune('\n')

	sign, err := authorize(bytes.ToLower(b.Bytes()), mKey)
	if err != nil {
		return err
	}

	buffers.Put(b)

	req.Header.Add(HeaderAuth, url.QueryEscape("type=master&ver=1.0&sig="+sign))

	return
}

// Add headers for query request
func (req *Request) QueryHeaders(len int) {
	req.Header.Add(HeaderContentType, "application/query+json")
	req.Header.Add(HeaderIsQuery, "true")
	req.Header.Add(HeaderContentLength, strconv.Itoa(len))
}

func parse(id string) (rId, rType string) {
	if strings.HasPrefix(id, "/") == false {
		id = "/" + id
	}
	if strings.HasSuffix(id, "/") == false {
		id = id + "/"
	}

	parts := strings.Split(id, "/")
	l := len(parts)

	if l%2 == 0 {
		rId = parts[l-2]
		rType = parts[l-3]
	} else {
		rId = parts[l-3]
		rType = parts[l-2]
	}
	return
}

func formatDate(t time.Time) string {
	t = t.UTC()
	return t.Format("Mon, 02 Jan 2006 15:04:05 GMT")
}

type queryPartitionKeyRangesRequest struct {
	Ranges []PartitionKeyRange `json:"PartitionKeyRanges,omitempty"`
	Count  int                 `json:"_count,omitempty"`
}
