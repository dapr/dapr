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
}

func (x *BulkPublishRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.GrpcServiceSpanAttributeKey] = diagConsts.DaprGRPCDaprService
	m[diagConsts.MessagingSystemSpanAttributeKey] = diagConsts.PubsubBuildingBlockType
	m[diagConsts.MessagingDestinationSpanAttributeKey] = x.GetTopic()
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

func (x *ConversationRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.RPCSystemSpanAttributeKey] = x.GetName()
}

func (x *ConversationRequestAlpha2) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	m[diagConsts.RPCSystemSpanAttributeKey] = x.GetName()
}

func (*DeleteJobRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*DecryptRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*DeleteBulkStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*EncryptRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*ExecuteActorStateTransactionRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*ExecuteStateTransactionRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetActorStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetBulkSecretRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetBulkStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetConfigurationRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetJobRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetMetadataRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*GetWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*InvokeActorRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*PauseWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*PurgeWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*QueryStateRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*RaiseEventWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*RegisterActorReminderRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*RegisterActorTimerRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*ResumeWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*ScheduleJobRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SetMetadataRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*ShutdownRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*StartWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubscribeConfigurationRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubscribeTopicEventsRequestInitialAlpha1) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubscribeTopicEventsRequestAlpha1) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODOalph
}

func (*SubscribeTopicEventsRequestAlpha1_InitialRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleDecryptRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleEncryptRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleGetKeyRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleSignRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleUnwrapKeyRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleVerifyRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*SubtleWrapKeyRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*TerminateWorkflowRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*TryLockRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*UnlockRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*UnregisterActorReminderRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*UnregisterActorTimerRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}

func (*UnsubscribeConfigurationRequest) AppendSpanAttributes(rpcMethod string, m map[string]string) {
	// TODO
}
