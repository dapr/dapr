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

package metadatahelper

import (
	"fmt"
	"strings"

	"github.com/dapr/dapr/pkg/modes"
)

// Shared helper func for metadata on pubsub.kafka && bindings.kafka to properly set the ClientID
func SetKafkaClientID(properties map[string]string, id string, namespace string, mode modes.DaprMode) {
	clientID := strings.TrimSpace(properties["clientID"])
	if clientID == "" || clientID == "sarama" {
		switch mode {
		case modes.KubernetesMode:
			clientID = fmt.Sprintf("%s.%s", namespace, id)
		case modes.StandaloneMode:
			clientID = id
		}
		properties["clientID"] = clientID
	}
}
