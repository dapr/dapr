/*
Copyright 2022 The Dapr Authors
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

package errutil

import (
	"fmt"
	"net/http"

	kitErrors "github.com/dapr/kit/errors"
	grpcCodes "google.golang.org/grpc/codes"
)

const (
	// DaprToDapr = "DAPR_RUNTIME_GRPC_"

	// COMPONENTS
	PubSub             = "DAPR_PUBSUB_"
	StateStore         = "DAPR_STATE_"
	Bindings           = "DAPR_BINDING_"
	SecretStore        = "DAPR_SECRET_"
	ConfigurationStore = "DAPR_CONFIGURATION_"
	Lock               = "DAPR_LOCK_"
	NameResolution     = "DAPR_NAME_RESOLUTION_"
	Middleware         = "DAPR_MIDDLEWARE_"

	// Generic
	ErrNotFound      = "NOT_FOUND"
	ErrNotConfigured = "NOT_CONFIGURED"
	ErrNameEmpty     = "Name_Empty"

	// Actor

	// Config

	// Crypto

	// Healthz

	// Invocation

	// Lock

	// PubSub
	// ErrPubSubNotFound = "pubsub %s not found" -> not needed since I have above

	// Secrets

	// State

	// Workflows
)

var (
	// Generic

	// Actor

	// Config

	// Crypto

	// Healthz

	// Invocation

	// Lock

	// PubSub
	ErrPubSubNotFound = kitErrors.New(
		grpcCodes.NotFound,
		http.StatusBadRequest,
		"pubsub %s is not found",
		fmt.Sprintf("%s%s", PubSub, ErrNotFound),
	)

	// ErrPubSubNotFound = &kitErrors.Error{
	// 	Message:  "pubsub %s is not found",
	// 	Tag:      PubSub + ErrNotFound,
	// 	HttpCode: http.StatusNotFound,
	// 	GrpcCode: grpcCodes.NotFound,
	// 	// Reason:   PubSub + ErrNotFound,
	// }

	ErrPubSubNotConfigured = &kitErrors.Error{
		Message:  "pubsub is not configured",
		Tag:      PubSub + ErrNotConfigured,
		HttpCode: http.StatusPreconditionFailed,
		GrpcCode: grpcCodes.FailedPrecondition,
		// Reason:   PubSub + ErrNotConfigured,
	}

	ErrPubSubNameEmpty = &kitErrors.Error{
		Message:  "pubsub name is empty",
		Tag:      PubSub + ErrNameEmpty,
		HttpCode: http.StatusNotFound,
		GrpcCode: grpcCodes.NotFound,
		// Reason:   PubSub + ErrNameEmpty,
	}

	ErrPubSubTopicEmpty = &kitErrors.Error{
		Message:  "topic is empty in pubsub %s",
		Tag:      PubSub + "Topic" + ErrNameEmpty,
		HttpCode: http.StatusNotFound,
		GrpcCode: grpcCodes.NotFound,
		// Reason:   PubSub + "Topic" + ErrNameEmpty,
	}

	ErrPubSubMetadataDeserialize = &kitErrors.Error{
		Message:  "failed deserializing metadata: %v",
		Tag:      "ERR_PUBSUB_REQUEST_METADATA",
		HttpCode: http.StatusBadRequest,
		GrpcCode: grpcCodes.InvalidArgument,
		// Reason:   "Unable to deserialize pubsub metadata",
	}

	ErrPubSubMsgDropped = &kitErrors.Error{
		Message:  "pubsub message dropped",
		Tag:      PubSub + "MSG_DROPPED",
		HttpCode: http.StatusNotFound,
		GrpcCode: grpcCodes.NotFound,
		// Reason:   "pubsub message dropped",
	}

	// Secrets

	// State

	// Workflows
)
