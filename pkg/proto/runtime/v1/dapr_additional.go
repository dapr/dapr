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

package runtime

import (
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"google.golang.org/protobuf/proto"
)

// This file contains additional, hand-written methods added to the generated objects.

// SubtleCryptoRequests is an interface for all Subtle*Request structs.
type SubtleCryptoRequests interface {
	// SetComponentName sets the value of the ComponentName property.
	SetComponentName(name string)
}

func (x *SubtleGetKeyRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleEncryptRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleDecryptRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleWrapKeyRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleUnwrapKeyRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleSignRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleVerifyRequest) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

// CryptoRequests is an interface for EncryptRequest and DecryptRequest.
type CryptoRequests interface {
	proto.Message

	// SetPayload sets the payload.
	SetPayload(payload *commonv1pb.StreamPayload)
	// GetPayload returns the payload.
	GetPayload() *commonv1pb.StreamPayload
	// Reset the object.
	Reset()
	// SetOptions sets the Options property.
	SetOptions(opts proto.Message)
	// HasOptions returns true if the Options property is not empty.
	HasOptions() bool
}

func (x *EncryptRequest) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *EncryptRequest) SetOptions(opts proto.Message) {
	if x == nil {
		return
	}

	x.Options = opts.(*EncryptRequestOptions)
}

func (x *EncryptRequest) HasOptions() bool {
	return x != nil && x.Options != nil
}

func (x *DecryptRequest) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *DecryptRequest) SetOptions(opts proto.Message) {
	if x == nil {
		return
	}

	x.Options = opts.(*DecryptRequestOptions)
}

func (x *DecryptRequest) HasOptions() bool {
	return x != nil && x.Options != nil
}

// CryptoResponses is an interface for EncryptResponse and DecryptResponse.
type CryptoResponses interface {
	proto.Message

	// SetPayload sets the payload.
	SetPayload(payload *commonv1pb.StreamPayload)
	// GetPayload returns the payload.
	GetPayload() *commonv1pb.StreamPayload
	// Reset the object.
	Reset()
}

func (x *EncryptResponse) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *DecryptResponse) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}
