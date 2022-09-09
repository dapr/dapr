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
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const configPrefix = "."

func writeTempConfig(path, content string) (delete func(), error error) {
	filePath := filepath.Join(configPrefix, path)
	err := os.WriteFile(filePath, []byte(content), fs.FileMode(0o644))
	return func() {
		os.Remove(filePath)
	}, err
}

func TestLoadPluggableComponentsFromFile(t *testing.T) {
	t.Run("valid pluggable component yaml content", func(t *testing.T) {
		filename := "test-pluggable-component-valid.yaml"
		yaml := `
apiVersion: dapr.io/v1alpha1
kind: PluggableComponent
metadata:
  name: mystate
spec:
  type: state
  version: v1
`
		remove, err := writeTempConfig(filename, yaml)
		require.NoError(t, err)
		defer remove()
		components, err := LoadFromDisk(configPrefix)
		require.NoError(t, err)
		assert.Len(t, components, 1)
	})

	t.Run("invalid yaml head", func(t *testing.T) {
		filename := "test-pluggable-component-invalid.yaml"
		yaml := `
INVALID_YAML_HERE
apiVersion: dapr.io/v1alpha1
kind: PluggableComponent
metadata:
name: statestore`
		remove, err := writeTempConfig(filename, yaml)
		require.NoError(t, err)
		defer remove()
		components, err := LoadFromDisk(configPrefix)
		require.NoError(t, err)
		assert.Len(t, components, 0)
	})

	t.Run("load pluggable components file not exist", func(t *testing.T) {
		components, err := LoadFromDisk("path-not-exists")

		assert.Nil(t, err)
		assert.Len(t, components, 0)
	})
}

const bufSize = 1024 * 1024

type fakeOperator struct {
	operatorv1pb.UnimplementedOperatorServer
	listPluggableComponentsCalled   atomic.Int64
	onListPluggableComponentsCalled func(*operatorv1pb.ListPluggableComponentsRequest)
	listPluggableComponentsErr      error
	listPluggableComponentsResp     *operatorv1pb.ListPluggableComponentsResponse
}

func (o *fakeOperator) ListPluggableComponents(ctx context.Context, in *operatorv1pb.ListPluggableComponentsRequest) (*operatorv1pb.ListPluggableComponentsResponse, error) {
	o.listPluggableComponentsCalled.Add(1)
	if o.onListPluggableComponentsCalled != nil {
		o.onListPluggableComponentsCalled(in)
	}
	return o.listPluggableComponentsResp, o.listPluggableComponentsErr
}

// getStateStore returns a state store connected to the given server
func getOperatorClient(srv *fakeOperator) (client operatorv1pb.OperatorClient, cleanup func(), err error) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, srv)
	go func() {
		if serveErr := s.Serve(lis); serveErr != nil {
			log.Debugf("Server exited with error: %v", serveErr)
		}
	}()
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	return operatorv1pb.NewOperatorClient(conn), func() {
		lis.Close()
		conn.Close()
	}, nil
}

func TestLoadPluggableComponentsK8S(t *testing.T) {
	t.Run("list pluggable components should return the namespace pluggable components", func(t *testing.T) {
		pc := v1alpha1.PluggableComponent{}
		pc.ObjectMeta.Name = "test"
		pc.ObjectMeta.Labels = map[string]string{
			"podName": "test",
		}
		pc.Spec = v1alpha1.PluggableComponentSpec{
			Type: string(components.State),
		}
		b, err := json.Marshal(&pc)
		require.NoError(t, err)

		svc := &fakeOperator{
			listPluggableComponentsResp: &operatorv1pb.ListPluggableComponentsResponse{
				PluggableComponents: [][]byte{b},
			},
		}
		client, cleanup, err := getOperatorClient(svc)

		require.NoError(t, err)
		defer cleanup()
		namespace := "testNamespace"
		podName := "testPodName"

		response, err := LoadFromKubernetes(namespace, podName, client)
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Equal(t, "test", response[0].Name)
		assert.Equal(t, components.State, response[0].Type)
		assert.Equal(t, int64(1), svc.listPluggableComponentsCalled.Load())
	})
	t.Run("list pluggable components should return error when error", func(t *testing.T) {
		pc := v1alpha1.PluggableComponent{}
		pc.ObjectMeta.Name = "test"
		pc.ObjectMeta.Labels = map[string]string{
			"podName": "test",
		}
		pc.Spec = v1alpha1.PluggableComponentSpec{
			Type: string(components.State),
		}
		svc := &fakeOperator{
			listPluggableComponentsErr: errors.New("fake-err"),
		}
		client, cleanup, err := getOperatorClient(svc)

		require.NoError(t, err)
		defer cleanup()
		namespace := "testNamespace"
		podName := "testPodName"

		response, err := LoadFromKubernetes(namespace, podName, client)
		assert.NotNil(t, err)
		assert.Nil(t, response)
		assert.Equal(t, int64(1), svc.listPluggableComponentsCalled.Load())
	})
}
