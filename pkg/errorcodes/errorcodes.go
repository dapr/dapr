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
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	domain           = "dapr.io"
	daprETagMismatch = "DAPR_STATE_ETAG_MISMATCH"
)

// New returns a Status representing c and msg.
func New(c codes.Code, msg string) (*status.Status, error) {
	ste := status.Newf(c, msg)
	ei := errdetails.ErrorInfo{
		Domain:   domain,
		Reason:   daprETagMismatch,
		Metadata: map[string]string{},
	}
	return ste.WithDetails(&ei)
}

// Newf returns New(c, fmt.Sprintf(format, a...)).
func Newf(c codes.Code, format string, a ...interface{}) (*status.Status, error) {
	return New(c, fmt.Sprintf(format, a...))
}

func StatusErrorJSON(st *status.Status) ([]byte, error) {
	return protojson.Marshal(st.Proto())
}
