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

// Package payloadstore offloads large workflow history payloads to a
// pluggable external store, replacing the inline value with a small
// in-band reference (see codec.go). The runtime carries no store
// implementation: with a nil Store the feature is a strict no-op and
// workflow behavior is byte-identical to a build without it. An embedder
// injects a Store through the workflow engine options to enable it.
package payloadstore

import (
	"context"
	"crypto/sha256"
)

// DefaultThreshold is the payload size in bytes at or above which
// implementations should offload a payload when no explicit threshold is
// configured.
const DefaultThreshold = 16 * 1024

// Reference identifies a payload held in an external payload store. It is
// what gets persisted in workflow history in place of the payload itself.
type Reference struct {
	// Checksum is the SHA-256 of the payload bytes. It makes the
	// reference content-addressed and lets readers detect tampering or
	// corruption of the stored payload.
	Checksum [sha256.Size]byte
	// Key is the store-assigned lookup key for the payload.
	Key string
	// Size is the payload length in bytes.
	Size uint64
}

// Store persists workflow payloads outside of workflow history and owns
// the policy for which payloads are worth offloading.
//
// Implementations must be safe for concurrent use. Put must be idempotent
// for identical data: offload runs again with the same payloads when a
// workflow turn is retried after a failed save.
type Store interface {
	// ShouldOffload reports whether a payload of the given size in bytes
	// should be offloaded to this store. It is consulted on the save hot
	// path for every new payload-bearing event, so it must be fast and
	// deterministic. Values that already encode a Reference are never
	// offered.
	ShouldOffload(size int) bool

	// Put stores data and returns a Reference whose Checksum is
	// sha256(data). instanceID is the workflow instance the payload
	// belongs to; stores may use it to partition payloads so a reference
	// forged into one instance's history cannot address another's data.
	Put(ctx context.Context, instanceID string, data []byte) (Reference, error)

	// Get returns the payload identified by ref. Implementations MUST
	// verify that the SHA-256 of the returned bytes equals ref.Checksum
	// and fail on mismatch, so a tampered or corrupted store surfaces as
	// an error rather than as silently wrong payload data.
	Get(ctx context.Context, ref Reference) ([]byte, error)
}
