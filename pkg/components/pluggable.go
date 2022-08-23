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
	"fmt"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Pluggable represents a pluggable component specification.
type Pluggable struct {
	// Name is the pluggable component name.
	Name string
	// Type is the component type.
	Type Type
	// Version is the pluggable component version.
	Version string
}

const defaultSocketFolder = "/var/run"

// socketPathFor returns a unique socket for the given component.
// the socket path will be composed by the pluggable component, name, version and type plus the component name.
func (p Pluggable) socketPathFor(componentName string) string {
	return fmt.Sprintf("%s/dapr-%s.%s-%s-%s.sock", defaultSocketFolder, p.Type, p.Name, p.Version, componentName)
}

// Connect returns a grpc connection for the pluggable component.
func (p Pluggable) Connect(componentName string, additionalOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	udsSocket := fmt.Sprintf("unix://%s", p.socketPathFor(componentName))
	opts := append(additionalOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if c, err := grpc.Dial(udsSocket, opts...); err != nil {
		return nil, errors.Wrapf(err, "unable to open GRPC connection using socket '%s'", udsSocket)
	} else {
		return c, nil
	}
}
