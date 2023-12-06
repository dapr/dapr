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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// InvokeMethodResponse holds InternalInvokeResponse protobuf message
// and provides the helpers to manage it.
type InvokeMethodResponse struct {
	replayableRequest
	r           *internalv1pb.InternalInvokeResponse
	dataTypeURL string
}

// NewInvokeMethodResponse returns new InvokeMethodResponse object with status.
func NewInvokeMethodResponse(statusCode int32, statusMessage string, statusDetails []*anypb.Any) *InvokeMethodResponse {
	return &InvokeMethodResponse{
		r: &internalv1pb.InternalInvokeResponse{
			Status:  &internalv1pb.Status{Code: statusCode, Message: statusMessage, Details: statusDetails},
			Message: &commonv1pb.InvokeResponse{},
		},
	}
}

// InternalInvokeResponse returns InvokeMethodResponse for InternalInvokeResponse pb to use the helpers.
func InternalInvokeResponse(pb *internalv1pb.InternalInvokeResponse) (*InvokeMethodResponse, error) {
	rsp := &InvokeMethodResponse{r: pb}
	if pb.GetMessage() == nil {
		pb.Message = &commonv1pb.InvokeResponse{Data: nil}
	}
	if pb.GetHeaders() == nil {
		pb.Headers = map[string]*internalv1pb.ListStringValue{}
	}

	return rsp, nil
}

// WithMessage sets InvokeResponse pb object to Message field.
func (imr *InvokeMethodResponse) WithMessage(pb *commonv1pb.InvokeResponse) *InvokeMethodResponse {
	imr.r.Message = pb
	return imr
}

// WithRawData sets message data from a readable stream.
func (imr *InvokeMethodResponse) WithRawData(data io.Reader) *InvokeMethodResponse {
	imr.ResetMessageData()
	imr.replayableRequest.WithRawData(data)
	return imr
}

// WithRawDataBytes sets message data from a []byte.
func (imr *InvokeMethodResponse) WithRawDataBytes(data []byte) *InvokeMethodResponse {
	return imr.WithRawData(bytes.NewReader(data))
}

// WithRawDataString sets message data from a string.
func (imr *InvokeMethodResponse) WithRawDataString(data string) *InvokeMethodResponse {
	return imr.WithRawData(strings.NewReader(data))
}

// WithContentType sets the content type.
func (imr *InvokeMethodResponse) WithContentType(contentType string) *InvokeMethodResponse {
	imr.r.Message.ContentType = contentType
	return imr
}

// WithDataTypeURL sets the type_url property for the data.
// When a type_url is set, the Content-Type automatically becomes the protobuf one.
func (imr *InvokeMethodResponse) WithDataTypeURL(val string) *InvokeMethodResponse {
	imr.dataTypeURL = val
	return imr
}

// WithHeaders sets gRPC response header metadata.
func (imr *InvokeMethodResponse) WithHeaders(headers metadata.MD) *InvokeMethodResponse {
	imr.r.Headers = internalv1pb.MetadataToInternalMetadata(headers)
	return imr
}

// WithFastHTTPHeaders populates HTTP response header to gRPC header metadata.
func (imr *InvokeMethodResponse) WithHTTPHeaders(headers map[string][]string) *InvokeMethodResponse {
	imr.r.Headers = internalv1pb.MetadataToInternalMetadata(headers)
	return imr
}

// WithFastHTTPHeaders populates fasthttp response header to gRPC header metadata.
func (imr *InvokeMethodResponse) WithFastHTTPHeaders(header *fasthttp.ResponseHeader) *InvokeMethodResponse {
	md := internalv1pb.FastHTTPHeadersToInternalMetadata(header)
	if len(md) > 0 {
		imr.r.Headers = md
	}
	return imr
}

// WithTrailers sets Trailer in internal InvokeMethodResponse.
func (imr *InvokeMethodResponse) WithTrailers(trailer metadata.MD) *InvokeMethodResponse {
	imr.r.Trailers = internalv1pb.MetadataToInternalMetadata(trailer)
	return imr
}

// WithReplay enables replaying for the data stream.
func (imr *InvokeMethodResponse) WithReplay(enabled bool) *InvokeMethodResponse {
	// If the object has data in-memory, WithReplay is a nop
	if !imr.HasMessageData() {
		imr.replayableRequest.SetReplay(enabled)
	}
	return imr
}

// CanReplay returns true if the data stream can be replayed.
func (imr *InvokeMethodResponse) CanReplay() bool {
	// We can replay if:
	// - The object has data in-memory
	// - The request is replayable
	return imr.HasMessageData() || imr.replayableRequest.CanReplay()
}

// Status gets Response status.
func (imr *InvokeMethodResponse) Status() *internalv1pb.Status {
	if imr.r == nil {
		return nil
	}
	return imr.r.GetStatus()
}

// IsHTTPResponse returns true if response status code is http response status.
func (imr *InvokeMethodResponse) IsHTTPResponse() bool {
	if imr.r == nil {
		return false
	}
	// gRPC status code <= 15 - https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
	// HTTP status code >= 100 - https://tools.ietf.org/html/rfc2616#section-10
	return imr.r.GetStatus().GetCode() >= 100
}

// Proto returns the internal InvokeMethodResponse Proto object.
func (imr *InvokeMethodResponse) Proto() *internalv1pb.InternalInvokeResponse {
	return imr.r
}

// ProtoWithData returns a copy of the internal InternalInvokeResponse Proto object with the entire data stream read into the Data property.
func (imr *InvokeMethodResponse) ProtoWithData() (*internalv1pb.InternalInvokeResponse, error) {
	if imr.r == nil {
		return nil, errors.New("message is nil")
	}

	// If the data is already in-memory in the object, return the object directly.
	// This doesn't copy the object, and that's fine because receivers are not expected to modify the received object.
	// Only reason for cloning the object below is to make ProtoWithData concurrency-safe.
	if imr.HasMessageData() {
		return imr.r, nil
	}

	// Clone the object
	m := proto.Clone(imr.r).(*internalv1pb.InternalInvokeResponse)

	// Read the data and store it in the object
	data, err := imr.RawDataFull()
	if err != nil || len(data) == 0 {
		return m, err
	}
	m.Message.Data = &anypb.Any{
		Value:   data,
		TypeUrl: imr.dataTypeURL, // Could be empty
	}

	return m, nil
}

// Headers gets Headers metadata.
func (imr *InvokeMethodResponse) Headers() DaprInternalMetadata {
	if imr.r == nil {
		return nil
	}
	return imr.r.GetHeaders()
}

// Trailers gets Trailers metadata.
func (imr *InvokeMethodResponse) Trailers() DaprInternalMetadata {
	if imr.r == nil {
		return nil
	}
	return imr.r.GetTrailers()
}

// Message returns message field in InvokeMethodResponse.
func (imr *InvokeMethodResponse) Message() *commonv1pb.InvokeResponse {
	if imr.r == nil {
		return nil
	}
	return imr.r.GetMessage()
}

// HasMessageData returns true if the message object contains a slice of data buffered.
func (imr *InvokeMethodResponse) HasMessageData() bool {
	m := imr.r.GetMessage()
	return len(m.GetData().GetValue()) > 0
}

// ResetMessageData resets the data inside the message object if present.
func (imr *InvokeMethodResponse) ResetMessageData() {
	if !imr.HasMessageData() {
		return
	}

	imr.r.GetMessage().GetData().Reset()
}

// ContenType returns the content type of the message.
func (imr *InvokeMethodResponse) ContentType() string {
	m := imr.r.GetMessage()
	if m == nil {
		return ""
	}

	contentType := m.GetContentType()

	// If there's a proto data and that has a type URL, or if we have a dataTypeUrl in the object, then the content type is the protobuf one
	if imr.dataTypeURL != "" || m.GetData().GetTypeUrl() != "" {
		return ProtobufContentType
	}

	return contentType
}

// RawData returns the stream body.
func (imr *InvokeMethodResponse) RawData() (r io.Reader) {
	// If the message has a data property, use that
	if imr.HasMessageData() {
		// HasMessageData() guarantees that the `imr.r.Message` and `imr.r.Message.Data` is not nil
		return bytes.NewReader(imr.r.GetMessage().GetData().GetValue())
	}

	return imr.replayableRequest.RawData()
}

// RawDataFull returns the entire data read from the stream body.
func (imr *InvokeMethodResponse) RawDataFull() ([]byte, error) {
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
