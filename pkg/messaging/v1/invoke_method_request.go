/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	// DefaultAPIVersion is the default Dapr API version.
	DefaultAPIVersion = internalv1pb.APIVersion_V1 //nolint:nosnakecase
)

// InvokeMethodRequest holds InternalInvokeRequest protobuf message
// and provides the helpers to manage it.
type InvokeMethodRequest struct {
	replayableRequest

	r *internalv1pb.InternalInvokeRequest
}

// NewInvokeMethodRequest creates InvokeMethodRequest object for method.
func NewInvokeMethodRequest(method string) *InvokeMethodRequest {
	return &InvokeMethodRequest{
		r: &internalv1pb.InternalInvokeRequest{
			Ver: DefaultAPIVersion,
			Message: &commonv1pb.InvokeRequest{
				Method: method,
			},
		},
	}
}

// FromInvokeRequestMessage creates InvokeMethodRequest object from InvokeRequest pb object.
func FromInvokeRequestMessage(pb *commonv1pb.InvokeRequest) *InvokeMethodRequest {
	req := &InvokeMethodRequest{
		r: &internalv1pb.InternalInvokeRequest{
			Ver:     DefaultAPIVersion,
			Message: pb,
		},
	}

	if pb != nil && pb.Data != nil && pb.Data.Value != nil {
		req.data = io.NopCloser(bytes.NewReader(pb.Data.Value))
		pb.Data.Reset()
	}

	return req
}

// InternalInvokeRequest creates InvokeMethodRequest object from InternalInvokeRequest pb object.
func InternalInvokeRequest(pb *internalv1pb.InternalInvokeRequest) (*InvokeMethodRequest, error) {
	req := &InvokeMethodRequest{r: pb}
	if pb.Message == nil {
		return nil, errors.New("field Message is nil")
	}

	if pb.Message.Data != nil && pb.Message.Data.Value != nil {
		req.data = io.NopCloser(bytes.NewReader(pb.Message.Data.Value))
		pb.Message.Data.Reset()
	}

	return req, nil
}

// WithActor sets actor type and id.
func (imr *InvokeMethodRequest) WithActor(actorType, actorID string) *InvokeMethodRequest {
	imr.r.Actor = &internalv1pb.Actor{ActorType: actorType, ActorId: actorID}
	return imr
}

// WithMetadata sets metadata.
func (imr *InvokeMethodRequest) WithMetadata(md map[string][]string) *InvokeMethodRequest {
	imr.r.Metadata = MetadataToInternalMetadata(md)
	return imr
}

// WithFastHTTPHeaders sets fasthttp request headers.
func (imr *InvokeMethodRequest) WithFastHTTPHeaders(header *fasthttp.RequestHeader) *InvokeMethodRequest {
	md := map[string][]string{}
	header.VisitAll(func(key []byte, value []byte) {
		md[string(key)] = []string{string(value)}
	})
	imr.r.Metadata = MetadataToInternalMetadata(md)
	return imr
}

// WithRawData sets message data from a readable stream.
func (imr *InvokeMethodRequest) WithRawData(data io.Reader) *InvokeMethodRequest {
	imr.replayableRequest.WithRawData(data)
	return imr
}

// WithRawDataBytes sets message data from a []byte.
func (imr *InvokeMethodRequest) WithRawDataBytes(data []byte) *InvokeMethodRequest {
	return imr.WithRawData(bytes.NewReader(data))
}

// WithRawDataString sets message data from a string.
func (imr *InvokeMethodRequest) WithRawDataString(data string) *InvokeMethodRequest {
	return imr.WithRawData(strings.NewReader(data))
}

// WithContentType sets the content type.
func (imr *InvokeMethodRequest) WithContentType(contentType string) *InvokeMethodRequest {
	imr.r.Message.ContentType = contentType
	return imr
}

// WithHTTPExtension sets new HTTP extension with verb and querystring.
//
//nolint:nosnakecase
func (imr *InvokeMethodRequest) WithHTTPExtension(verb string, querystring string) *InvokeMethodRequest {
	httpMethod, ok := commonv1pb.HTTPExtension_Verb_value[strings.ToUpper(verb)]
	if !ok {
		httpMethod = int32(commonv1pb.HTTPExtension_POST)
	}

	imr.r.Message.HttpExtension = &commonv1pb.HTTPExtension{
		Verb:        commonv1pb.HTTPExtension_Verb(httpMethod),
		Querystring: querystring,
	}

	return imr
}

// WithCustomHTTPMetadata applies a metadata map to a InvokeMethodRequest.
func (imr *InvokeMethodRequest) WithCustomHTTPMetadata(md map[string]string) *InvokeMethodRequest {
	for k, v := range md {
		if imr.r.Metadata == nil {
			imr.r.Metadata = make(map[string]*internalv1pb.ListStringValue)
		}

		// NOTE: We don't explicitly lowercase the keys here but this will be done
		//       later when attached to the HTTP request as headers.
		imr.r.Metadata[k] = &internalv1pb.ListStringValue{Values: []string{v}}
	}

	return imr
}

// WithReplay enables replaying for the data stream.
func (imr *InvokeMethodRequest) WithReplay(enabled bool) *InvokeMethodRequest {
	imr.replayableRequest.SetReplay(enabled)
	return imr
}

// EncodeHTTPQueryString generates querystring for http using http extension object.
func (imr *InvokeMethodRequest) EncodeHTTPQueryString() string {
	m := imr.r.Message
	if m == nil || m.HttpExtension == nil {
		return ""
	}

	return m.HttpExtension.Querystring
}

// APIVersion gets API version of InvokeMethodRequest.
func (imr *InvokeMethodRequest) APIVersion() internalv1pb.APIVersion {
	return imr.r.GetVer()
}

// Metadata gets Metadata of InvokeMethodRequest.
func (imr *InvokeMethodRequest) Metadata() DaprInternalMetadata {
	return imr.r.Metadata
}

// Proto returns InternalInvokeRequest Proto object.
func (imr *InvokeMethodRequest) Proto() *internalv1pb.InternalInvokeRequest {
	return imr.r
}

// ProtoWithData returns a copy of the internal InvokeMethodRequest Proto object with the entire data stream read into the Data property.
func (imr *InvokeMethodRequest) ProtoWithData() (*internalv1pb.InternalInvokeRequest, error) {
	if imr.r == nil || imr.r.Message == nil {
		return nil, errors.New("message is nil")
	}

	// If the data has already been read, return it right away
	if imr.HasMessageData() {
		return imr.r, nil
	}

	// Read the data and store it in the object
	data, err := imr.RawDataFull()
	if err != nil {
		return imr.r, err
	}
	imr.r.Message.Data = &anypb.Any{
		Value: data,
	}

	// Close the source data stream and replay buffers
	err = imr.replayableRequest.Close()
	if err != nil {
		return imr.r, err
	}

	return imr.r, nil
}

// Actor returns actor type and id.
func (imr *InvokeMethodRequest) Actor() *internalv1pb.Actor {
	return imr.r.Actor
}

// Message gets InvokeRequest Message object.
func (imr *InvokeMethodRequest) Message() *commonv1pb.InvokeRequest {
	return imr.r.Message
}

// HasMessageData returns true if the message object contains a slice of data buffered.
func (imr *InvokeMethodRequest) HasMessageData() bool {
	m := imr.r.Message
	return m != nil && m.Data != nil && len(m.Data.Value) > 0
}

// ContenType returns the content type of the message.
func (imr *InvokeMethodRequest) ContentType() string {
	m := imr.r.Message
	if m == nil {
		return ""
	}

	return m.GetContentType()
}

// RawData returns the stream body.
// Note: this method is not safe for concurrent use.
func (imr *InvokeMethodRequest) RawData() (r io.Reader) {
	m := imr.r.Message
	if m == nil {
		return nil
	}

	// If the message has a data property, use that
	if imr.HasMessageData() {
		return bytes.NewReader(m.Data.Value)
	}

	return imr.replayableRequest.RawData()
}

// RawDataFull returns the entire data read from the stream body.
func (imr *InvokeMethodRequest) RawDataFull() ([]byte, error) {
	// If the message has a data property, use that
	if imr.HasMessageData() {
		return imr.r.Message.Data.Value, nil
	}

	r := imr.RawData()
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// Adds a new header to the existing set.
func (imr *InvokeMethodRequest) AddHeaders(header *fasthttp.RequestHeader) {
	md := map[string][]string{}
	header.VisitAll(func(key []byte, value []byte) {
		md[string(key)] = []string{string(value)}
	})

	internalMd := MetadataToInternalMetadata(md)

	if imr.r.Metadata == nil {
		imr.r.Metadata = internalMd
	} else {
		for key, val := range internalMd {
			// We're only adding new values, not overwriting existing
			if _, ok := imr.r.Metadata[key]; !ok {
				imr.r.Metadata[key] = val
			}
		}
	}
}
