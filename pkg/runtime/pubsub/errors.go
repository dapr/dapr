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
