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
	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
)

// This file contains additional, hand-written methods added to the generated objects.

func (x *InvokeServiceRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCServiceInvocationService
	m[diagConsts.NetPeerNameSpanAttributeKey] = x.GetId()
	m[diagConsts.DaprAPISpanNameInternal] = "CallLocal/" + x.GetId() + "/" + x.GetMessage().GetMethod()
}

func (x *PublishEventRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.MessagingSystemSpanAttributeKey] = diagConsts.PubsubBuildingBlockType
	m[diagConsts.MessagingDestinationSpanAttributeKey] = x.GetTopic()
	m[diagConsts.MessagingDestinationKindSpanAttributeKey] = diagConsts.MessagingDestinationTopicKind
}

func (x *BulkPublishRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.MessagingSystemSpanAttributeKey] = diagConsts.PubsubBuildingBlockType
	m[diagConsts.MessagingDestinationSpanAttributeKey] = x.GetTopic()
	m[diagConsts.MessagingDestinationKindSpanAttributeKey] = diagConsts.MessagingDestinationTopicKind
}

func (x *InvokeBindingRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.DBNameSpanAttributeKey] = x.GetName()
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.BindingBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = rpcMethod
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.BindingBuildingBlockType
}

func (x *GetStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.DBNameSpanAttributeKey] = x.GetStoreName()
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = rpcMethod
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
}

func (x *SaveStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.DBNameSpanAttributeKey] = x.GetStoreName()
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = rpcMethod
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
}

func (x *DeleteStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.DBNameSpanAttributeKey] = x.GetStoreName()
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.StateBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = rpcMethod
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.StateBuildingBlockType
}

func (x *GetSecretRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.DBNameSpanAttributeKey] = x.GetStoreName()
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.DBSystemSpanAttributeKey] = diagConsts.SecretBuildingBlockType
	m[diagConsts.DBStatementSpanAttributeKey] = rpcMethod
	m[diagConsts.DBConnectionStringSpanAttributeKey] = diagConsts.SecretBuildingBlockType
}
