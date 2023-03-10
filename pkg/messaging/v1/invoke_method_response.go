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
	r *internalv1pb.InternalInvokeResponse
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
	if pb.Message == nil {
		pb.Message = &commonv1pb.InvokeResponse{Data: nil}
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

// WithHeaders sets gRPC response header metadata.
func (imr *InvokeMethodResponse) WithHeaders(headers metadata.MD) *InvokeMethodResponse {
	imr.r.Headers = MetadataToInternalMetadata(headers)
	return imr
}

// WithFastHTTPHeaders populates HTTP response header to gRPC header metadata.
func (imr *InvokeMethodResponse) WithHTTPHeaders(headers map[string][]string) *InvokeMethodResponse {
	imr.r.Headers = MetadataToInternalMetadata(headers)
	return imr
}

// WithFastHTTPHeaders populates fasthttp response header to gRPC header metadata.
func (imr *InvokeMethodResponse) WithFastHTTPHeaders(header *fasthttp.ResponseHeader) *InvokeMethodResponse {
	md := DaprInternalMetadata{}
	header.VisitAll(func(key []byte, value []byte) {
		md[string(key)] = &internalv1pb.ListStringValue{
			Values: []string{string(value)},
		}
	})
	if len(md) > 0 {
		imr.r.Headers = md
	}
	return imr
}

// WithTrailers sets Trailer in internal InvokeMethodResponse.
func (imr *InvokeMethodResponse) WithTrailers(trailer metadata.MD) *InvokeMethodResponse {
	imr.r.Trailers = MetadataToInternalMetadata(trailer)
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
	return imr.r.Status
}

// IsHTTPResponse returns true if response status code is http response status.
func (imr *InvokeMethodResponse) IsHTTPResponse() bool {
	if imr.r == nil {
		return false
	}
	// gRPC status code <= 15 - https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
	// HTTP status code >= 100 - https://tools.ietf.org/html/rfc2616#section-10
	return imr.r.Status.Code >= 100
}

// Proto returns the internal InvokeMethodResponse Proto object.
func (imr *InvokeMethodResponse) Proto() *internalv1pb.InternalInvokeResponse {
	return imr.r
}

// ProtoWithData returns a copy of the internal InternalInvokeResponse Proto object with the entire data stream read into the Data property.
func (imr *InvokeMethodResponse) ProtoWithData() (*internalv1pb.InternalInvokeResponse, error) {
	if imr.r == nil || imr.r.Message == nil {
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
	if err != nil {
		return m, err
	}
	m.Message.Data = &anypb.Any{
		Value: data,
	}

	return m, nil
}

// Headers gets Headers metadata.
func (imr *InvokeMethodResponse) Headers() DaprInternalMetadata {
	if imr.r == nil {
		return nil
	}
	return imr.r.Headers
}

// Trailers gets Trailers metadata.
func (imr *InvokeMethodResponse) Trailers() DaprInternalMetadata {
	if imr.r == nil {
		return nil
	}
	return imr.r.Trailers
}

// Message returns message field in InvokeMethodResponse.
func (imr *InvokeMethodResponse) Message() *commonv1pb.InvokeResponse {
	if imr.r == nil {
		return nil
	}
	return imr.r.Message
}

// HasMessageData returns true if the message object contains a slice of data buffered.
func (imr *InvokeMethodResponse) HasMessageData() bool {
	m := imr.r.Message
	return m != nil && m.Data != nil && len(m.Data.Value) > 0
}

// ResetMessageData resets the data inside the message object if present.
func (imr *InvokeMethodResponse) ResetMessageData() {
	if !imr.HasMessageData() {
		return
	}

	imr.r.Message.Data.Reset()
}

// ContenType returns the content type of the message.
func (imr *InvokeMethodResponse) ContentType() string {
	m := imr.r.Message
	if m == nil {
		return ""
	}

	contentType := m.ContentType

	if m.Data != nil && m.Data.TypeUrl != "" {
		contentType = ProtobufContentType
	}

	return contentType
}

// RawData returns the stream body.
func (imr *InvokeMethodResponse) RawData() (r io.Reader) {

	// If the message has a data property, use that
	if imr.HasMessageData() {
		// HasMessageData() guarantees that the `imr.r.Message` and `imr.r.Message.Data` is not nil
		return bytes.NewReader(imr.r.Message.Data.Value)
	}

	return imr.replayableRequest.RawData()
}

// RawDataFull returns the entire data read from the stream body.
func (imr *InvokeMethodResponse) RawDataFull() ([]byte, error) {
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
