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

package pluggable

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var (
	discoveryLog        = logger.NewLogger("pluggable-components-discovery")
	onServiceDiscovered map[string]func(name string, dialer GRPCConnectionDialer)

	// discoveryRetryInterval is how often the socket folder is re-checked while waiting for component sockets to appear.
	discoveryRetryInterval = 500 * time.Millisecond
	// discoveryTimeout bounds how long discovery waits for component sockets to appear before giving up.
	// Only applies when the socket folder exists (i.e. pluggable components are configured); a missing
	// folder still returns immediately.
	discoveryTimeout = 30 * time.Second
)

func init() {
	onServiceDiscovered = make(map[string]func(name string, dialer GRPCConnectionDialer))
}

// AddServiceDiscoveryCallback adds a callback function that should be called when the given service was discovered.
func AddServiceDiscoveryCallback(serviceName string, callbackFunc func(name string, dialer GRPCConnectionDialer)) {
	onServiceDiscovered[serviceName] = callbackFunc
}

// removeExt removes file extension
func removeExt(fileName string) string {
	return fileName[:len(fileName)-len(filepath.Ext(fileName))]
}

const (
	SocketFolderEnvVar  = "DAPR_COMPONENTS_SOCKETS_FOLDER"
	defaultSocketFolder = "/tmp/dapr-components-sockets"
)

// GetSocketFolderPath returns the shared unix domain socket folder path
func GetSocketFolderPath() string {
	return utils.GetEnvOrElse(SocketFolderEnvVar, defaultSocketFolder)
}

type service struct {
	// protoRef is the proto service name
	protoRef string
	// componentName is the component name that implements such service.
	componentName string
	// dialer is the used grpc connectiondialer.
	dialer GRPCConnectionDialer
}

type reflectServiceClient interface {
	ListServices() ([]string, error)
	Reset()
}
type grpcConnectionCloser interface {
	grpc.ClientConnInterface
	Close() error
}

// waitForComponentSockets blocks until at least one non-directory entry shows up
// under dir, or discoveryTimeout elapses. The injector appends pluggable component
// containers after daprd in spec.containers and the kubelet does not wait for them,
// so their sockets may not exist yet when discovery runs its first read. Without
// this wait, slow components (e.g. Python-based ones needing a few hundred ms to
// listen) are missed for the lifetime of the process. It returns early when ctx
// is canceled.
func waitForComponentSockets(ctx context.Context, dir string) {
	ticker := time.NewTicker(discoveryRetryInterval)
	defer ticker.Stop()
	deadline := time.Now().Add(discoveryTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(dir)
			if err == nil {
				for _, entry := range entries {
					if !entry.IsDir() {
						return
					}
				}
			}
		}
	}
}

// serviceDiscovery returns all available discovered pluggable components services.
// uses gRPC reflection package to list implemented services.
func serviceDiscovery(ctx context.Context, reflectClientFactory func(string) (reflectServiceClient, func(), error), dialOpts ...grpc.DialOption) ([]service, error) {
	services := []service{}
	componentsSocketPath := GetSocketFolderPath()
	_, err := os.Stat(componentsSocketPath)

	if os.IsNotExist(err) { // not exists is the same as empty.
		return services, nil
	}

	// The socket folder exists, so pluggable components are configured, but their
	// containers may still be starting. Wait for their sockets instead of doing a
	// single read that misses them.
	waitForComponentSockets(ctx, componentsSocketPath)

	log.Debugf("loading pluggable components under path %s", componentsSocketPath)
	if err != nil {
		return nil, err
	}

	files, err := os.ReadDir(componentsSocketPath)
	if err != nil {
		return nil, fmt.Errorf("could not list pluggable components unix sockets: %w", err)
	}

	for _, dirEntry := range files {
		if dirEntry.IsDir() { // skip dirs
			continue
		}

		f, err := dirEntry.Info()
		if err != nil {
			return nil, err
		}

		socket := filepath.Join(componentsSocketPath, f.Name())
		if !utils.IsSocket(f) {
			discoveryLog.Warnf("could not use socket for file %s", socket)
			continue
		}

		refctClient, cleanup, err := reflectClientFactory(socket)
		if err != nil {
			return nil, err
		}
		defer cleanup()

		serviceList, err := refctClient.ListServices()
		if err != nil {
			return nil, fmt.Errorf("unable to list services: %w", err)
		}
		//nolint:staticcheck
		dialer := socketDialer(socket, append([]grpc.DialOption{grpc.WithBlock(), grpc.FailOnNonTempDialError(true)}, dialOpts...)...)

		componentName := removeExt(f.Name())
		for _, svc := range serviceList {
			services = append(services, service{
				componentName: componentName,
				protoRef:      svc,
				dialer:        dialer,
			})
		}
	}
	log.Debugf("found %d pluggable component services", len(services)-1) // reflection api doesn't count.
	return services, nil
}

// callback invoke callback function for each given service
func callback(services []service) {
	for _, service := range services {
		callback, ok := onServiceDiscovered[service.protoRef]
		if !ok { // ignoring unknown service
			continue
		}
		callback(service.componentName, service.dialer)
		log.Infof("pluggable component '%s' was successfully registered for '%s'", service.componentName, service.protoRef)
	}
}

// reflectServiceConnectionCloser is used for cleanup the stream created to be used for the reflection service.
func reflectServiceConnectionCloser(conn grpcConnectionCloser, client reflectServiceClient) func() {
	return func() {
		client.Reset()
		conn.Close()
	}
}

// Discover discover the pluggable components and callback the service discovery with the given component name and grpc dialer.
func Discover(ctx context.Context, maxRecvMsgSize int) error {
	services, err := serviceDiscovery(ctx, func(socket string) (reflectServiceClient, func(), error) {
		conn, err := SocketDial(
			ctx,
			socket,
			grpc.WithBlock(), //nolint:staticcheck
		)
		if err != nil {
			return nil, nil, err
		}
		client := grpcreflect.NewClientV1Alpha(ctx, reflectpb.NewServerReflectionClient(conn))
		return client, reflectServiceConnectionCloser(conn, client), nil
	}, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)))
	if err != nil {
		return err
	}

	callback(services)
	return nil
}
