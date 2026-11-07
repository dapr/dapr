/*
Copyright 2026 The Dapr Authors
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

package errors

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type closed struct {
	method string
}

func NewClosed(method string) error {
	return &closed{method: method}
}

func (c *closed) Error() string {
	return "actor is closed, cannot handle " + c.method
}

func IsClosed(err error) bool {
	var c *closed
	return errors.As(err, &c)
}

// NewClosedUnavailable is NewClosed with a gRPC Unavailable status attached.
// It reports as closed to IsClosed, so the local actor routers keep treating
// it as retryable, and it reports as codes.Unavailable to status.FromError,
// so the remote actor routers and language SDKs treat it as transient
// instead of surfacing gRPC UNKNOWN. Use it when an actor is closed by a
// lifecycle event (placement rebalancing, scale-to-zero, shutdown/restart)
// rather than by the workflow itself.
func NewClosedUnavailable(method string) error {
	return &closedUnavailable{closed: &closed{method: method}}
}

type closedUnavailable struct {
	*closed
}

// GRPCStatus implements google.golang.org/grpc/status's GRPCStatus interface.
func (c *closedUnavailable) GRPCStatus() *status.Status {
	return status.New(codes.Unavailable, c.closed.Error())
}

func (c *closedUnavailable) Unwrap() error {
	return c.closed
}
