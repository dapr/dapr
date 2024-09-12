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

package pluggable

import (
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	kiterrors "github.com/dapr/kit/errors"
)

type ErrorConverter func(status.Status) error

// Compose together two errors converters by applying the inner first and if the error was not converted, then it applies to the outer.
func (outer ErrorConverter) Compose(inner ErrorConverter) ErrorConverter {
	return func(s status.Status) error {
		err := inner(s)
		st, ok := status.FromError(err)
		if ok {
			return outer(*st)
		}
		return err
	}
}

// MethodErrorConverter represents a simple map that maps from a grpc statuscode to a domain-level error.
type MethodErrorConverter map[codes.Code]ErrorConverter

func (m MethodErrorConverter) Merge(other MethodErrorConverter) MethodErrorConverter {
	n := MethodErrorConverter{}

	for k, v := range m {
		n[k] = v
	}

	for k, v := range other {
		converter, ok := n[k]
		if !ok {
			n[k] = v
		} else { // compose converter in case of two errors has the same grpc status code.
			n[k] = converter.Compose(v)
		}
	}
	return n
}

const (
	kitErrorField = "kit_error"
	httpCodeField = "http_code"
	tagField      = "tag"
)

func StatusFromKitError(
	grpcCode codes.Code,
	httpCode int,
	message string,
	tag string,
	detail *errdetails.ErrorInfo,
) (*status.Status, error) {
	return status.New(grpcCode, message).WithDetails(&errdetails.ErrorInfo{
		Reason: detail.GetReason(),
		Domain: detail.GetDomain(),
		Metadata: map[string]string{
			kitErrorField: "true",
			httpCodeField: strconv.Itoa(httpCode),
			tagField:      tag,
		},
	})
}

func MustStatusErrorFromKitError(
	grpcCode codes.Code,
	httpCode int,
	message string,
	tag string,
	detail *errdetails.ErrorInfo,
) error {
	s, err := StatusFromKitError(grpcCode, httpCode, message, tag, detail)
	if err != nil {
		panic(err)
	}

	return s.Err()
}

func KitErrorFromStatus(s *status.Status) (error, bool) {
	details := s.Details()
	if len(details) != 1 {
		return s.Err(), false
	}

	info, ok := details[0].(*errdetails.ErrorInfo)
	if !ok || info.GetMetadata()[kitErrorField] != "true" {
		return s.Err(), false
	}

	httpCode, err := strconv.Atoi(info.GetMetadata()[httpCodeField])
	if err != nil {
		return s.Err(), false
	}

	return kiterrors.NewBuilder(s.Code(), httpCode, s.Message(), info.GetMetadata()[tagField]).
		WithDetails(&errdetails.ErrorInfo{
			Domain: info.GetDomain(),
			Reason: info.GetReason(),
		}).Build(), true
}

func KitErrorFromGrpcError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}

	s, ok := status.FromError(err)
	if !ok {
		return err, false
	}

	kerr, ok := KitErrorFromStatus(s)
	if !ok {
		return err, false
	}

	return kerr, true
}

func KitErrorFromGrpcErrorIgnore(err error) error {
	s, ok := status.FromError(err)
	if !ok {
		return err
	}

	kerr, _ := KitErrorFromStatus(s)
	return kerr
}
