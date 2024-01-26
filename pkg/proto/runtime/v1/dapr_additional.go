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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// This file contains additional, hand-written methods added to the generated objects.

func (x *InvokeActorRequest) ToInternalInvokeRequest() *internalv1pb.InternalInvokeRequest {
	if x == nil {
		return nil
	}

	r := &internalv1pb.InternalInvokeRequest{
		Ver: internalv1pb.APIVersion_V1,
		Message: &commonv1pb.InvokeRequest{
			Method: x.Method,
		},
	}

	if len(x.Data) > 0 {
		r.Message.Data = &anypb.Any{
			Value: x.Data,
		}
	}

	if x.ActorType != "" && x.ActorId != "" {
		r.Actor = &internalv1pb.Actor{
			ActorType: x.ActorType,
			ActorId:   x.ActorId,
		}
	}

	if len(x.Metadata) > 0 {
		r.Metadata = make(map[string]*internalv1pb.ListStringValue, len(x.Metadata))
		for k, v := range x.Metadata {
			r.Metadata[k] = &internalv1pb.ListStringValue{
				Values: []string{v},
			}
		}
	}

	return r
}

// WorkflowRequests is an interface for all a number of *WorkflowRequest structs.
type WorkflowRequests interface {
	// SetWorkflowComponent sets the value of the WorkflowComponent property.
	SetWorkflowComponent(val string)
	// SetInstanceId sets the value of the InstanceId property.
	SetInstanceId(val string)
}

func (x *GetWorkflowRequest) SetWorkflowComponent(val string) {
	if x != nil {
		x.WorkflowComponent = val
	}
}

func (x *GetWorkflowRequest) SetInstanceId(val string) {
	if x != nil {
		x.InstanceId = val
	}
}

func (x *TerminateWorkflowRequest) SetWorkflowComponent(val string) {
	if x != nil {
		x.WorkflowComponent = val
	}
}

func (x *TerminateWorkflowRequest) SetInstanceId(val string) {
	if x != nil {
		x.InstanceId = val
	}
}

func (x *PauseWorkflowRequest) SetWorkflowComponent(val string) {
	if x != nil {
		x.WorkflowComponent = val
	}
}

func (x *PauseWorkflowRequest) SetInstanceId(val string) {
	if x != nil {
		x.InstanceId = val
	}
}

func (x *ResumeWorkflowRequest) SetWorkflowComponent(val string) {
	if x != nil {
		x.WorkflowComponent = val
	}
}

func (x *ResumeWorkflowRequest) SetInstanceId(val string) {
	if x != nil {
		x.InstanceId = val
	}
}

func (x *PurgeWorkflowRequest) SetWorkflowComponent(val string) {
	if x != nil {
		x.WorkflowComponent = val
	}
}

func (x *PurgeWorkflowRequest) SetInstanceId(val string) {
	if x != nil {
		x.InstanceId = val
	}
}

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
