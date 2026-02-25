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
	"context"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/utils"
)

type Event any

type PlacementReconnect struct {
	ActorTypes *[]string
}

type UpdateTypes struct {
	ActorTypes []string
}

type ReportHost struct {
	Host *v1pb.Host
}

type StreamOrder struct {
	Order *v1pb.PlacementOrder
	IDx   uint64
}

type StreamSend struct {
	Host *v1pb.Host
}

type LookupRequest struct {
	Request  *api.LookupActorRequest
	Context  context.Context
	Response chan<- *LookupResponse
}

type LookupResponse struct {
	Response *api.LookupActorResponse
	Context  context.Context
	Cancel   context.CancelCauseFunc
	Error    error
}

type LockRequest struct {
	Context  context.Context
	Response chan<- *LockResponse
}

type LockResponse struct {
	Context context.Context
	Cancel  context.CancelCauseFunc
}

type ConnCloseStream struct {
	Error error
	IDx   uint64
}

type Shutdown struct {
	Error error
}

type DisseminationTimeout struct {
	Version uint64
}

type SetDrainOngoingCallTimeout struct {
	Drain   *bool
	Timeout *time.Duration
}

func IsActorLocal(targetActorAddress, hostAddress string, port string) bool {
	if targetActorAddress == hostAddress+":"+port {
		// Easy case when there is a perfect match
		return true
	}

	if utils.IsLocalhost(hostAddress) && strings.HasSuffix(targetActorAddress, ":"+port) {
		return utils.IsLocalhost(targetActorAddress[0 : len(targetActorAddress)-len(port)-1])
	}

	return false
}
