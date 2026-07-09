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

package payloadstore

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// refMagic marks a payload string as an encoded Reference. References
// live in-band in the same proto3 string fields that carry user payloads,
// so the magic must make confusing user data for a reference practically
// impossible:
//
//   - Payload fields are proto3 strings and therefore always valid UTF-8,
//     for user payloads and encoded references alike. The magic uses only
//     single-byte control characters, which are valid UTF-8, so encoded
//     references survive proto marshaling.
//   - The leading NUL cannot occur at the start of any JSON, XML, or other
//     textual payload, and the full 38-byte sequence must match exactly.
//     An accidental false positive requires user data to begin with this
//     precise control-byte-framed sequence, which does not occur in
//     natural data.
//   - A payload deliberately crafted to look like a reference is skipped
//     by the offload pass and persisted verbatim, exactly as the sender
//     wrote it. Dereferencing it later either fails (unknown key or
//     checksum mismatch, failing only that workflow) or returns bytes
//     whose SHA-256 the sender already knew, so no new information is
//     disclosed. Stores can additionally partition payloads by the
//     instanceID given to Put to rule out cross-instance addressing.
const refMagic = "\x00\x01dapr.workflow.payload.reference.v1\x01\x00"

// ErrNotReference is returned by DecodeReference when the value does not
// carry the reference magic prefix, i.e. it is ordinary user payload data.
// Any other error means the value claims to be a reference but is corrupt.
var ErrNotReference = errors.New("payload is not an encoded payload-store reference")

// refJSON is the wire form of a Reference following the magic prefix. The
// checksum is hex-encoded to keep the whole encoding valid UTF-8.
type refJSON struct {
	Key      string `json:"k"`
	Checksum string `json:"c"`
	Size     uint64 `json:"s"`
}

// EncodeReference renders ref as an in-band string that IsReference
// detects and DecodeReference parses. The output is deterministic and
// valid UTF-8, safe for proto3 string payload fields.
func EncodeReference(ref Reference) string {
	body, err := json.Marshal(refJSON{
		Key:      ref.Key,
		Checksum: hex.EncodeToString(ref.Checksum[:]),
		Size:     ref.Size,
	})
	if err != nil {
		// json.Marshal of a struct of string/uint64 fields cannot fail.
		panic(fmt.Sprintf("payloadstore: failed to marshal reference: %v", err))
	}
	return refMagic + string(body)
}

// IsReference cheaply reports whether s carries the reference magic
// prefix. A true result does not guarantee the reference is well formed;
// use DecodeReference for a strict parse.
func IsReference(s string) bool {
	return strings.HasPrefix(s, refMagic)
}

// DecodeReference strictly parses an encoded Reference. It returns
// ErrNotReference when s has no magic prefix (ordinary user data), and a
// descriptive error when the magic is present but the body is malformed
// (a corrupt or forged reference).
func DecodeReference(s string) (Reference, error) {
	if !IsReference(s) {
		return Reference{}, ErrNotReference
	}

	raw := s[len(refMagic):]
	var body refJSON
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return Reference{}, fmt.Errorf("malformed payload-store reference body: %w", err)
	}
	// The body must be exactly one JSON value with nothing after it, not
	// even whitespace (which EncodeReference never emits): the decode must
	// have consumed every byte.
	if dec.InputOffset() != int64(len(raw)) {
		return Reference{}, errors.New("malformed payload-store reference body: trailing data")
	}

	checksum, err := hex.DecodeString(body.Checksum)
	if err != nil {
		return Reference{}, fmt.Errorf("malformed payload-store reference checksum: %w", err)
	}

	ref := Reference{
		Key:  body.Key,
		Size: body.Size,
	}
	if len(checksum) != len(ref.Checksum) {
		return Reference{}, fmt.Errorf("malformed payload-store reference checksum: got %d bytes, want %d", len(checksum), len(ref.Checksum))
	}
	copy(ref.Checksum[:], checksum)

	return ref, nil
}
