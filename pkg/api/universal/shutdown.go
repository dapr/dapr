/*
Copyright 2022 The Dapr Authors
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

package universal

import (
	"context"
	"strings"
	"time"

	grpcMetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// ForceShutdownMetadataKey is the gRPC metadata key (and HTTP header, after
// transport bridging) that, when set to "true", causes the sidecar to exit
// immediately via os.Exit(1) instead of performing a graceful shutdown.
const ForceShutdownMetadataKey = "dapr-force-shutdown"

// forceExitDelay gives the response a chance to flush to the client before
// the process exits on the force path.
const forceExitDelay = 100 * time.Millisecond

// Shutdown the sidecar.
func (a *Universal) Shutdown(ctx context.Context, _ *runtimev1pb.ShutdownRequest) (*emptypb.Empty, error) {
	if isForceShutdown(ctx) {
		a.logger.Warn("Force shutdown requested, exiting immediately without graceful shutdown")
		go func() {
			time.Sleep(forceExitDelay)
			a.exitFn(1)
		}()
		return &emptypb.Empty{}, nil
	}

	a.logger.Info("Shutdown requested via API")
	go a.shutdownFn()
	return &emptypb.Empty{}, nil
}

func isForceShutdown(ctx context.Context) bool {
	md, ok := grpcMetadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, v := range md.Get(ForceShutdownMetadataKey) {
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return false
}
