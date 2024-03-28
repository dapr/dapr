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

package pubsub

import (
	"github.com/dapr/components-contrib/pubsub"
	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/dapr/tests/integration/framework/socket"
)

type options struct {
	socket *socket.Socket
	pubsub pubsub.PubSub
	pmrCh  <-chan *compv1pb.PullMessagesResponse
}

func WithSocket(socket *socket.Socket) Option {
	return func(o *options) {
		o.socket = socket
	}
}

func WithPubSub(pubsub pubsub.PubSub) Option {
	return func(o *options) {
		o.pubsub = pubsub
	}
}

func WithPullMessagesChannel(ch <-chan *compv1pb.PullMessagesResponse) Option {
	return func(o *options) {
		o.pmrCh = ch
	}
}
