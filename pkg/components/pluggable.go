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

const defaultSocketFolder = "/var/run/dapr"

// Connect returns a grpc connection for the pluggable component.
func (p Pluggable) Connect(componentName string) (*grpc.ClientConn, error) {
	socket := fmt.Sprintf("unix://%s/%s.%s-%s-%s", defaultSocketFolder, p.Type, p.Name, p.Version, componentName)
	if c, err := grpc.Dial(socket, grpc.WithTransportCredentials(insecure.NewCredentials())); err != nil {
		return nil, errors.Wrapf(err, "unable to open GRPC connection using socket '%s'", socket)
	} else {
		return c, nil
	}
}
