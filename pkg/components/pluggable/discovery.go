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
	"os"
	"path/filepath"

	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"

	"github.com/pkg/errors"

	"github.com/jhump/protoreflect/grpcreflect"

	"google.golang.org/grpc"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

var (
	discoveryLog        = logger.NewLogger("pluggable-components-discovery")
	onServiceDiscovered map[string]func(name string, dialer GRPCConnectionDialer)
)

func init() {
	onServiceDiscovered = make(map[string]func(name string, dialer GRPCConnectionDialer))
}

// AddServiceV1DiscoveryCallback adds a callback function that should be called when the given service was discovered.
func AddServiceV1DiscoveryCallback(serviceName string, callbackFunc func(name string, dialer GRPCConnectionDialer)) {
	onServiceDiscovered[withV1(serviceName)] = callbackFunc
}

// removeExt removes file extension
func removeExt(fileName string) string {
	return fileName[:len(fileName)-len(filepath.Ext(fileName))]
}

const (
	SocketFolderEnvVar  = "DAPR_PLUGGABLE_COMPONENTS_SOCKETS_FOLDER"
	defaultSocketFolder = "/dapr-components-sockets"
)

// GetSocketFolderPath returns the shared unix domain socket folder path
func GetSocketFolderPath() string {
	return utils.GetEnvOrElse(SocketFolderEnvVar, defaultSocketFolder)
}

const (
	// the current components proto version
	protoV1      = "v1"
	protoPackage = "dapr.proto.components." + protoV1
)

// withV1 adds the service name using the v1 package.
func withV1(serviceName string) string {
	return protoPackage + "." + serviceName
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
}

// serviceDiscovery returns all available discovered pluggable components services.
// uses gRPC reflection package to list implemented services.
func serviceDiscovery(reflectClientFactory func(string) (reflectServiceClient, *grpc.ClientConn, error)) ([]service, error) {
	services := []service{}
	componentsSocketPath := GetSocketFolderPath()
	_, err := os.Stat(componentsSocketPath)

	if os.IsNotExist(err) { // not exists is the same as empty.
		return services, nil
	}

	log.Debugf("loading pluggable components under path %s", componentsSocketPath)
	if err != nil {
		return nil, err
	}

	files, err := os.ReadDir(componentsSocketPath)
	if err != nil {
		return nil, errors.Wrap(err, "could not list pluggable components unix sockets")
	}

	for _, dirEntry := range files {
		if dirEntry.IsDir() { // skip dirs
			continue
		}

		f, err := dirEntry.Info()
		if err != nil {
			return nil, err
		}

		absPath := filepath.Join(componentsSocketPath, f.Name())
		if !utils.IsSocket(f) {
			discoveryLog.Warnf("could not use socket for file %s", absPath)
			continue
		}

		refctClient, conn, err := reflectClientFactory(absPath)
		if err != nil {
			return nil, err
		}

		serviceList, err := refctClient.ListServices()
		if err != nil {
			return nil, errors.Wrap(err, "unable to list services")
		}

		componentName := removeExt(f.Name())
		dialer := func() (*grpc.ClientConn, error) {
			return conn, nil
		}
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

// Discover discover the pluggable components and callback the service discovery with the given component name and grpc dialer.
func Discover(ctx context.Context) error {
	services, err := serviceDiscovery(func(socket string) (reflectServiceClient, *grpc.ClientConn, error) {
		conn, err := SocketDial(ctx, socket, grpc.WithBlock())
		if err != nil {
			return nil, nil, err
		}
		return grpcreflect.NewClient(ctx, reflectpb.NewServerReflectionClient(conn)), conn, nil
	})
	if err != nil {
		return err
	}

	callback(services)
	return nil
}
