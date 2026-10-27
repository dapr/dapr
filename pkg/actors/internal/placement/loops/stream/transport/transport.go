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

// Package transport abstracts the placement stream wire protocol behind a
// protocol-neutral interface so the placement client loops are identical for
// the v1 (standalone placement service, ReportDaprStatus) and v2 (scheduler,
// ReportActorTypes) protocols.
package transport

import (
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

// Transport is a single established placement stream.
type Transport interface {
	// Recv blocks for the next placement order from the server. It returns
	// an error for malformed or unknown messages, which closes the stream.
	Recv() (*loops.Order, error)

	// SendReport reports the host presence and hosted actor types.
	SendReport(*loops.Report) error

	// SendAck acknowledges a placement order phase.
	SendAck(*loops.Ack) error

	// CloseSend closes the sending side of the stream.
	CloseSend() error
}
