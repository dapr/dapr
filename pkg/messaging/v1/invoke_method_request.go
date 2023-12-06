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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/valyala/fasthttp"
	"google.golang.org/protobuf/proto"
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

	r           *internalv1pb.InternalInvokeRequest
	dataObject  any
	dataTypeURL string
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
	return &InvokeMethodRequest{
		r: &internalv1pb.InternalInvokeRequest{
			Ver:     DefaultAPIVersion,
			Message: pb,
		},
	}
}

// FromInternalInvokeRequest creates InvokeMethodRequest object from FromInternalInvokeRequest pb object.
func FromInternalInvokeRequest(pb *internalv1pb.InternalInvokeRequest) (*InvokeMethodRequest, error) {
	req := &InvokeMethodRequest{r: pb}
	if pb.GetMessage() == nil {
		return nil, errors.New("field Message is nil")
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
	imr.r.Metadata = internalv1pb.MetadataToInternalMetadata(md)
	return imr
}

// WithHTTPHeaders sets metadata from HTTP request headers.
func (imr *InvokeMethodRequest) WithHTTPHeaders(header http.Header) *InvokeMethodRequest {
	imr.r.Metadata = internalv1pb.HTTPHeadersToInternalMetadata(header)
	return imr
}

// WithFastHTTPHeaders sets metadata from fasthttp request headers.
func (imr *InvokeMethodRequest) WithFastHTTPHeaders(header *fasthttp.RequestHeader) *InvokeMethodRequest {
	imr.r.Metadata = internalv1pb.FastHTTPHeadersToInternalMetadata(header)
	return imr
}

// WithRawData sets message data from a readable stream.
func (imr *InvokeMethodRequest) WithRawData(data io.Reader) *InvokeMethodRequest {
	imr.dataObject = nil
	imr.ResetMessageData()
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

// WithDataObject sets message from an object which will be serialized as JSON
func (imr *InvokeMethodRequest) WithDataObject(data any) *InvokeMethodRequest {
	enc, _ := json.Marshal(data)
	res := imr.WithRawDataBytes(enc)
	res.dataObject = data
	return res
}

// WithContentType sets the content type.
func (imr *InvokeMethodRequest) WithContentType(contentType string) *InvokeMethodRequest {
	imr.r.Message.ContentType = contentType
	return imr
}

// WithDataTypeURL sets the type_url property for the data.
// When a type_url is set, the Content-Type automatically becomes the protobuf one.
func (imr *InvokeMethodRequest) WithDataTypeURL(val string) *InvokeMethodRequest {
	imr.dataTypeURL = val
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
		if imr.r.GetMetadata() == nil {
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
	// If the object has data in-memory, WithReplay is a nop
	if !imr.HasMessageData() {
		imr.replayableRequest.SetReplay(enabled)
	}
	return imr
}

// CanReplay returns true if the data stream can be replayed.
func (imr *InvokeMethodRequest) CanReplay() bool {
	// We can replay if:
	// - The object has data in-memory
	// - The request is replayable
	return imr.HasMessageData() || imr.replayableRequest.CanReplay()
}

// EncodeHTTPQueryString generates querystring for http using http extension object.
func (imr *InvokeMethodRequest) EncodeHTTPQueryString() string {
	m := imr.r.GetMessage()
	if m.GetHttpExtension() == nil {
		return ""
	}

	return m.GetHttpExtension().GetQuerystring()
}

// APIVersion gets API version of InvokeMethodRequest.
func (imr *InvokeMethodRequest) APIVersion() internalv1pb.APIVersion {
	return imr.r.GetVer()
}

// Metadata gets Metadata of InvokeMethodRequest.
func (imr *InvokeMethodRequest) Metadata() DaprInternalMetadata {
	return imr.r.GetMetadata()
}

// Proto returns InternalInvokeRequest Proto object.
func (imr *InvokeMethodRequest) Proto() *internalv1pb.InternalInvokeRequest {
	return imr.r
}

// ProtoWithData returns a copy of the internal InternalInvokeRequest Proto object with the entire data stream read into the Data property.
func (imr *InvokeMethodRequest) ProtoWithData() (*internalv1pb.InternalInvokeRequest, error) {
	if imr.r == nil || imr.r.GetMessage() == nil {
		return nil, errors.New("message is nil")
	}

	// If the data is already in-memory in the object, return the object directly.
	// This doesn't copy the object, and that's fine because receivers are not expected to modify the received object.
	// Only reason for cloning the object below is to make ProtoWithData concurrency-safe.
	if imr.HasMessageData() {
		return imr.r, nil
	}

	// Clone the object
	m := proto.Clone(imr.r).(*internalv1pb.InternalInvokeRequest)

	// Read the data and store it in the object
	data, err := imr.RawDataFull()
	if err != nil {
		return m, err
	}
	m.Message.Data = &anypb.Any{
		Value:   data,
		TypeUrl: imr.dataTypeURL, // Could be empty
	}

	return m, nil
}

// Actor returns actor type and id.
func (imr *InvokeMethodRequest) Actor() *internalv1pb.Actor {
	return imr.r.GetActor()
}

// Message gets InvokeRequest Message object.
func (imr *InvokeMethodRequest) Message() *commonv1pb.InvokeRequest {
	return imr.r.GetMessage()
}

// HasMessageData returns true if the message object contains a slice of data buffered.
func (imr *InvokeMethodRequest) HasMessageData() bool {
	m := imr.r.GetMessage()
	return len(m.GetData().GetValue()) > 0
}

// ResetMessageData resets the data inside the message object if present.
func (imr *InvokeMethodRequest) ResetMessageData() {
	if !imr.HasMessageData() {
		return
	}

	imr.r.GetMessage().GetData().Reset()
}

// ContenType returns the content type of the message.
func (imr *InvokeMethodRequest) ContentType() string {
	m := imr.r.GetMessage()
	if m == nil {
		return ""
	}

	// If there's a proto data and that has a type URL, or if we have a dataTypeUrl in the object, then the content type is the protobuf one
	if imr.dataTypeURL != "" || m.GetData().GetTypeUrl() != "" {
		return ProtobufContentType
	}

	return m.GetContentType()
}

// RawData returns the stream body.
// Note: this method is not safe for concurrent use.
func (imr *InvokeMethodRequest) RawData() (r io.Reader) {
	m := imr.r.GetMessage()
	if m == nil {
		return nil
	}

	// If the message has a data property, use that
	if imr.HasMessageData() {
		return bytes.NewReader(m.GetData().GetValue())
	}

	return imr.replayableRequest.RawData()
}

// RawDataFull returns the entire data read from the stream body.
func (imr *InvokeMethodRequest) RawDataFull() ([]byte, error) {
	// If the message has a data property, use that
	if imr.HasMessageData() {
		return imr.r.GetMessage().GetData().GetValue(), nil
	}

	r := imr.RawData()
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(r)
}

// GetDataObject returns the data object stored in the object
func (imr *InvokeMethodRequest) GetDataObject() any {
	return imr.dataObject
}

// AddMetadata adds new metadata options to the existing set.
func (imr *InvokeMethodRequest) AddMetadata(md map[string][]string) {
	if imr.r.GetMetadata() == nil {
		imr.WithMetadata(md)
		return
	}

	for key, val := range internalv1pb.MetadataToInternalMetadata(md) {
		// We're only adding new values, not overwriting existing
		if _, ok := imr.r.GetMetadata()[key]; !ok {
			imr.r.Metadata[key] = val
		}
	}
}
