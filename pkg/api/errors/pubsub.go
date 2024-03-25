/*
Copyright 2024  The Dapr Authors
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

	"google.golang.org/grpc/codes"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/kit/errors"
)

type PubSubError struct {
	name string
}

type PubSubMetadataError struct {
	p                *PubSubError
	metadata         map[string]string
	skipResourceInfo bool
}

type PubSubTopicError struct {
	p     *PubSubMetadataError
	topic string
}

func PubSub(name string) *PubSubError {
	return &PubSubError{
		name: name,
	}
}

func (p *PubSubError) WithMetadata(metadata map[string]string) *PubSubMetadataError {
	return &PubSubMetadataError{
		p:        p,
		metadata: metadata,
	}
}

func (p *PubSubError) WithAppError(appID string, err error) *PubSubMetadataError {
	meta := map[string]string{
		"appID": appID,
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	return &PubSubMetadataError{
		p:        p,
		metadata: meta,
	}
}

func (p *PubSubError) withTopicError(topic string, err error) *PubSubMetadataError {
	return &PubSubMetadataError{
		p: p,
		metadata: map[string]string{
			"topic": topic,
			"error": err.Error(),
		},
	}
}

func (p PubSubError) PublishMessage(topic string, err error) error {
	return p.withTopicError(topic, err).build(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error when publishing to topic %s in pubsub %s: %s", topic, p.name, err),
		"ERR_PUBSUB_PUBLISH_MESSAGE",
		"PUBLISH_MESSAGE",
	)
}

func (p *PubSubError) PublishForbidden(topic, appID string, err error) error {
	return p.withTopicError(topic, err).build(
		codes.PermissionDenied,
		http.StatusForbidden,
		fmt.Sprintf("topic %s is not allowed for app id %s", topic, appID),
		"ERR_PUBSUB_FORBIDDEN",
		"FORBIDDEN",
	)
}

// This is specifically for the error we are expecting for the api_tests. The not found
// expected error codes are different than the existing ones for PubSubNotFound, hence
// why this one is needed
func (p PubSubError) TestNotFound(topic string, err error) error {
	return p.withTopicError(topic, err).build(
		codes.NotFound,
		http.StatusBadRequest,
		fmt.Sprintf("pubsub '%s' not found", p.name),
		"ERR_PUBSUB_NOT_FOUND",
		"TEST_NOT_FOUND",
	)
}

func (p *PubSubMetadataError) NotFound() error {
	p.skipResourceInfo = true
	return p.build(
		codes.InvalidArgument,
		http.StatusNotFound,
		fmt.Sprintf("%s %s is not found", metadata.PubSubType, p.p.name),
		"ERR_PUBSUB_NOT_FOUND",
		errors.CodeNotFound,
	)
}

func (p *PubSubMetadataError) NotConfigured() error {
	p.skipResourceInfo = true
	return p.build(
		codes.FailedPrecondition,
		http.StatusBadRequest,
		fmt.Sprintf("%s %s is not configured", metadata.PubSubType, p.p.name),
		"ERR_PUBSUB_NOT_CONFIGURED",
		errors.CodeNotConfigured,
	)
}

func (p *PubSubMetadataError) WithTopic(topic string) *PubSubTopicError {
	return &PubSubTopicError{
		p:     p,
		topic: topic,
	}
}

func (p *PubSubMetadataError) NameEmpty() error {
	return p.build(
		codes.InvalidArgument,
		http.StatusNotFound,
		"pubsub name is empty",
		"ERR_PUBSUB_EMPTY",
		"NAME_EMPTY",
	)
}

func (p *PubSubMetadataError) TopicEmpty() error {
	return p.build(
		codes.InvalidArgument,
		http.StatusNotFound,
		fmt.Sprintf("topic is empty in pubsub %s", p.p.name),
		"ERR_TOPIC_NAME_EMPTY",
		"TOPIC_NAME_EMPTY",
	)
}

func (p *PubSubMetadataError) DeserializeError(err error) error {
	return p.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("failed deserializing metadata. Error: %s", err),
		"ERR_PUBSUB_REQUEST_METADATA",
		"METADATA_DESERIALIZATION",
	)
}

func (p *PubSubMetadataError) CloudEventCreation() error {
	return p.build(
		codes.InvalidArgument,
		http.StatusInternalServerError,
		"cannot create cloudevent",
		"ERR_PUBSUB_CLOUD_EVENTS_SER",
		"CLOUD_EVENT_CREATION",
	)
}

func (p *PubSubTopicError) MarshalEnvelope() error {
	msg := fmt.Sprintf(
		"error when marshalling cloud event envelope for topic %s pubsub %s: %s",
		p.topic, p.p.p.name, p.p.metadata["error"],
	)
	return p.p.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_PUBSUB_EVENTS_SER",
		"MARSHAL_ENVELOPE",
	)
}

func (p *PubSubTopicError) MarshalEvents() error {
	message := fmt.Sprintf(
		"error marshaling events to bytes for topic %s pubsub %s. error: %s",
		p.topic, p.p.p.name, p.p.metadata["error"],
	)

	return p.p.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_PUBSUB_EVENTS_SER",
		"MARSHAL_EVENTS",
	)
}

// UnmarshalEvents only occurs in http/api.go
func (p *PubSubTopicError) UnmarshalEvents(err error) error {
	message := fmt.Sprintf(
		"error when unmarshaling the request for topic %s pubsub %s: %s",
		p.topic, p.p.p.name, err,
	)

	return p.p.build(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"ERR_PUBSUB_EVENTS_SER",
		"UNMARSHAL_EVENTS",
	)
}

func (p *PubSubMetadataError) build(grpcCode codes.Code, httpCode int, msg, tag, errCode string) error {
	err := errors.NewBuilder(grpcCode, httpCode, msg, tag)
	if !p.skipResourceInfo {
		err = err.WithResourceInfo(string(metadata.PubSubType), p.p.name, "", msg)
	}
	return err.WithErrorInfo(
		errors.CodePrefixPubSub+errCode,
		p.metadata,
	).Build()
}

func PubSubOutbox(appID string, err error) error {
	message := fmt.Sprintf("error while publishing outbox message: %s", err)
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		"ERR_PUBLISH_OUTBOX",
	).WithErrorInfo(errors.CodePrefixPubSub+"OUTBOX", map[string]string{
		"appID": appID, "error": err.Error(),
	}).Build()
}
