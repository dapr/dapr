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

func (x *SubtleGetKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleEncryptAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleDecryptAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleWrapKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleUnwrapKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleSignAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleVerifyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

// CryptoRequests is an interface for EncryptAlpha1Request and DecryptAlpha1Request.
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

func (x *EncryptAlpha1Request) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *EncryptAlpha1Request) SetOptions(opts proto.Message) {
	if x == nil {
		return
	}

	x.Options = opts.(*EncryptAlpha1RequestOptions)
}

func (x *EncryptAlpha1Request) HasOptions() bool {
	return x != nil && x.Options != nil
}

func (x *DecryptAlpha1Request) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *DecryptAlpha1Request) SetOptions(opts proto.Message) {
	if x == nil {
		return
	}

	x.Options = opts.(*DecryptAlpha1RequestOptions)
}

func (x *DecryptAlpha1Request) HasOptions() bool {
	return x != nil && x.Options != nil
}

// CryptoResponses is an interface for EncryptAlpha1Response and DecryptAlpha1Response.
type CryptoResponses interface {
	proto.Message

	// SetPayload sets the payload.
	SetPayload(payload *commonv1pb.StreamPayload)
	// GetPayload returns the payload.
	GetPayload() *commonv1pb.StreamPayload
	// Reset the object.
	Reset()
}

func (x *EncryptAlpha1Response) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}

func (x *DecryptAlpha1Response) SetPayload(payload *commonv1pb.StreamPayload) {
	if x == nil {
		return
	}

	x.Payload = payload
}
