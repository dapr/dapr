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

package grpc

import (
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// SubscribeTopicEvents is called by the Dapr runtime to ad hoc stream
// subscribe to topics. If gRPC API server closes, returns func early with nil
// to close stream.
func (a *api) SubscribeTopicEventsAlpha1(stream runtimev1pb.Dapr_SubscribeTopicEventsAlpha1Server) error {
	errCh := make(chan error, 2)
	subDone := make(chan struct{})
	a.wg.Add(2)
	go func() {
		errCh <- a.processor.PubSub().Streamer().Subscribe(stream)
		close(subDone)
		a.wg.Done()
	}()
	go func() {
		select {
		case <-a.closeCh:
		case <-subDone:
		}
		errCh <- nil
		a.wg.Done()
	}()

	return <-errCh
}
