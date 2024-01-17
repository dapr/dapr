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

package errors

import (
	"fmt"
	"net/http"

	grpcCodes "google.golang.org/grpc/codes"

	kiterrors "github.com/dapr/kit/errors"
)

const (
	InFixTopic                     = "TOPIC"
	PostFixNameEmpty               = "NAME_EMPTY"
	PostFixMetadataDeserialization = "METADATA_DESERIALIZATION"
	PostFixPublishMessage          = "PUBLISH_MESSAGE"
	PostFixCloudEventCreation      = "CLOUD_EVENT_CREATION"
	PostFixMarshalEnvelope         = "MARSHAL_ENVELOPE"
	PostFixMarshalEvents           = "MARSHAL_EVENTS"
	PostFixUnMarshalEvents         = "UNMARSHAL_EVENTS"
	PostFixForbidden               = "FORBIDDEN"
	PostFixOutbox                  = "OUTBOX"
)

func PubSubNameEmpty(name string, pubsubType string, metadata map[string]string) error {
	message := "pubsub name is empty"
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_PUBSUB_EMPTY",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixNameEmpty, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubTopicEmpty(name string, pubsubType string, metadata map[string]string) error {
	message := fmt.Sprintf("topic is empty in pubsub %s", name)
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusNotFound,
		message,
		"ERR_TOPIC_EMPTY",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+InFixTopic+PostFixNameEmpty, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubMetadataDeserialize(name string, pubsubType string, metadata map[string]string, err error) error {
	message := fmt.Sprintf("failed deserializing metadata. Error: %s", err.Error())
	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_PUBSUB_REQUEST_METADATA",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixMetadataDeserialization, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubPublishMessage(name string, pubsubType string, topic string, err error) error {
	message := fmt.Sprintf("error when publishing to topic %s in pubsub %s: %s", topic, name, err.Error())
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_PUBSUB_PUBLISH_MESSAGE",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixPublishMessage, map[string]string{"topic": topic, "error": err.Error()}).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubCloudEventCreation(name string, pubsubType string, metadata map[string]string) error {
	message := "cannot create cloudevent"

	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusInternalServerError,
		message,
		"ERR_PUBSUB_CLOUD_EVENTS_SER",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixCloudEventCreation, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubMarshalEnvelope(name string, topic string, pubsubType string, metadata map[string]string) error {
	message := fmt.Sprintf("error when marshalling cloud event envelope for topic %s pubsub %s", topic, name)

	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusInternalServerError,
		message,
		"ERR_PUBSUB_CLOUD_EVENTS_SER",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixMarshalEnvelope, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubMarshalEvents(name string, pubsubType string, topic string, metadata map[string]string) error {
	message := fmt.Sprintf("error marshaling events to bytes for topic %s pubsub %s", topic, name)

	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		message+". error: "+metadata["error"],
		"ERR_PUBSUB_EVENTS_SER",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixMarshalEvents, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

// PubSubUnMarshalEvents only occurs in http/api.go
func PubSubUnMarshalEvents(name string, pubsubType string, topic string, metadata map[string]string, err error) error {
	message := fmt.Sprintf("error when unmarshaling the request for topic %s pubsub %s: %s", topic, name, err.Error())

	return kiterrors.NewBuilder(
		grpcCodes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_PUBSUB_EVENTS_SER",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixUnMarshalEvents, metadata).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubPublishForbidden(name string, pubsubType string, topic string, appID string, err error) error {
	message := fmt.Sprintf("topic %s is not allowed for app id %s", topic, appID)
	return kiterrors.NewBuilder(
		grpcCodes.PermissionDenied,
		http.StatusForbidden,
		message,
		"ERR_PUBSUB_FORBIDDEN",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixForbidden, map[string]string{"topic": topic, "error": err.Error()}).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

// This is specifically for the error we are expecting for the api_tests. The not found
// expected error codes are different than the existing ones for PubSubNotFound, hence
// why this one is needed
func PubSubTestNotFound(name string, pubsubType string, topic string, err error) error {
	message := fmt.Sprintf("pubsub '%s' not found", name)
	return kiterrors.NewBuilder(
		grpcCodes.NotFound,
		http.StatusBadRequest,
		message,
		"ERR_PUBSUB_NOT_FOUND",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+"TEST"+kiterrors.CodeNotFound, map[string]string{"topic": topic, "error": err.Error()}).
		WithResourceInfo(pubsubType, name, "", message).
		Build()
}

func PubSubOubox(appID string, err error) error {
	message := fmt.Sprintf("error while publishing outbox message: %s", err.Error())
	return kiterrors.NewBuilder(
		grpcCodes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_PUBLISH_OUTBOX",
	).
		WithErrorInfo(kiterrors.CodePrefixPubSub+PostFixOutbox, map[string]string{"appID": appID, "error": err.Error()}).
		Build()
}
