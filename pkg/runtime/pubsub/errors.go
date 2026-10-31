/*
Copyright 2021 The Dapr Authors
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

package pubsub

import (
	"errors"
	"fmt"

	"github.com/dapr/dapr/pkg/messages"
)

var ErrMessageDropped = errors.New("pubsub message dropped") // TODO: remove this and use apierrors.PubSubMsgDropped

// ErrSubscriptionClosed is returned when a message is published to a
// subscription that is being closed (e.g. during graceful shutdown or
// hot-reload). Callers should avoid NACKing the message.
var ErrSubscriptionClosed = errors.New("subscription is closed")

// MessageError wraps a pubsub delivery error with optional app-provided details
// that can be forwarded to a dead-letter publish when configured.
type MessageError struct {
	err     error
	details string
}

// NewMessageError returns err annotated with app-provided details.
// If details is empty, err is returned unchanged. If err is nil and details is
// non-empty, details becomes the base error text. Error() preserves the base
// error string so existing log/test expectations stay stable; callers that need
// the app text should use Details() / ErrorDetails().
func NewMessageError(err error, details string) error {
	if details == "" {
		return err
	}
	if err == nil {
		err = errors.New(details)
	}
	return &MessageError{err: err, details: details}
}

// NewMessageDroppedError returns ErrMessageDropped, optionally annotated with
// app-provided details while preserving errors.Is(..., ErrMessageDropped).
func NewMessageDroppedError(details string) error {
	return NewMessageError(ErrMessageDropped, details)
}

func (e *MessageError) Error() string {
	return e.err.Error()
}

func (e *MessageError) Unwrap() error {
	return e.err
}

// Details returns the app-provided error text, if any.
func (e *MessageError) Details() string {
	return e.details
}

// ErrorDetails extracts app-provided error details from err's chain, if present.
func ErrorDetails(err error) string {
	var me *MessageError
	if errors.As(err, &me) {
		return me.Details()
	}
	return ""
}

// pubsub.NotFoundError is returned by the runtime when the pubsub does not exist.
type NotFoundError struct {
	PubsubName string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("pubsub '%s' not found", e.PubsubName)
}

// pubsub.NotAllowedError is returned by the runtime when publishing is forbidden.
type NotAllowedError struct {
	Topic string
	ID    string
}

func (e NotAllowedError) Error() string {
	return fmt.Sprintf(messages.ErrPubsubForbidden, e.Topic, e.ID)
}
