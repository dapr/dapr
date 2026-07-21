/*
Copyright 2026 The Dapr Authors
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

package loops

import (
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

type ReloadPubSub struct {
	*catbase
	Name   string
	Result chan<- error
}

type StopPubSub struct {
	*catbase
	Name string
	Done chan struct{}
}

type StartAppSubscriptions struct {
	*catbase
	Result chan<- error
}

type StopAppSubscriptions struct {
	*catbase
	Done chan struct{}
}

type StopAllSubscriptionsForever struct {
	*catbase
	Done chan struct{}
}

type StartStreamerSubscription struct {
	*catbase
	Subscription *subapi.Subscription
	ConnectionID rtpubsub.ConnectionID
	Result       chan<- error
}

type StopStreamerSubscription struct {
	*catbase
	Subscription *subapi.Subscription
	ConnectionID rtpubsub.ConnectionID
	Done         chan struct{}
}

type ReloadDeclaredAppSubscription struct {
	*catbase
	Name       string
	PubSubName string
	Result     chan<- error
}

type InitProgrammaticSubscriptions struct {
	*catbase
	Result chan<- error
}
