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
	contrib_pubsub "github.com/dapr/components-contrib/pubsub"
)

// Adapter is the interface for message buses.
type Adapter interface {
	GetPubSub(pubsubName string) contrib_pubsub.PubSub
	Publish(req *contrib_pubsub.PublishRequest) error
}
