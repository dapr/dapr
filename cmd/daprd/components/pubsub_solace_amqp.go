//go:build allcomponents

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

package components

import (
	"github.com/dapr/components-contrib/pubsub/amqp"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
)

// pubsub.solace.amqp is superseded by pubsub.amqp, which speaks the same
// protocol without the Solace addressing defaults. It stays registered so that
// existing configurations keep loading. The docs describe it as the
// compatibility name and point new components at pubsub.amqp.
func init() {
	pubsubLoader.DefaultRegistry.RegisterComponent(amqp.NewSolaceAMQPPubsub, "solace.amqp")
}
