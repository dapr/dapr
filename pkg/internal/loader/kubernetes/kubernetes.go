/*
Copyright 2024 The Dapr Authors
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

package kubernetes

import (
	"time"

	config "github.com/dapr/dapr/pkg/config/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.loader.kubernetes")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
)

type Options struct {
	Config    config.KubernetesConfig
	Client    operatorv1pb.OperatorClient
	Namespace string
	PodName   string
}
