/*
Copyright 2023 The Dapr Authors
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

package errorcodes

import (
	"google.golang.org/protobuf/encoding/protojson"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type errorCodesReason string

const (
	domain                       = "dapr.io"
	ErrorCodesFeatureMetadataKey = "error_codes_feature"
	EtagMismatch                 = errorCodesReason("DAPR_STATE_ETAG_MISMATCH")
	PubSubTopicNotFound          = errorCodesReason("DAPR_PUBSUB_TOPIC_NOT_FOUND")
)

// NewStatusError returns a Status representing Code, error Reason, Message and optional Metadata.
// When successful, it returns a Status with Details. Otherwise, nil with error.
func NewStatusError(c codes.Code, reason errorCodesReason, msg string, metadata map[string]string) (*status.Status, error) {
	ste := status.New(c, msg)
	md := metadata
	if md == nil {
		md = map[string]string{}
	}
	ei := errdetails.ErrorInfo{
		Domain:   domain,
		Reason:   string(reason),
		Metadata: md,
	}
	return ste.WithDetails(&ei)
}

func StatusErrorJSON(st *status.Status) ([]byte, error) {
	return protojson.Marshal(st.Proto())
}
