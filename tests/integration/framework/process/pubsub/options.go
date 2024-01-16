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

import "github.com/dapr/components-contrib/pubsub"

type options struct {
	socketDir string
	pubsub    pubsub.PubSub
}

func WithSocketDirectory(dir string) Option {
	return func(o *options) {
		o.socketDir = dir
	}
}

func WithPubSub(pubsub pubsub.PubSub) Option {
	return func(o *options) {
		o.pubsub = pubsub
	}
}
