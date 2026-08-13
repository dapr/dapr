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

package runtime

import (
	"context"
	"errors"
	"fmt"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/modes"
)

func (a *DaprRuntime) loadMCPServers(ctx context.Context) error {
	var l loader.Loader[mcpserverapi.MCPServer]

	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		l = kubernetes.NewMCPServers(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
		})
	case modes.StandaloneMode:
		l = disk.NewMCPServers(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}

	log.Info("Loading MCP servers…")

	servers, err := l.Load(ctx)
	if err != nil {
		return err
	}

	authorizedServers, ok := a.authz.GetAuthorizedObjects(servers, a.authz.IsObjectAuthorized).([]mcpserverapi.MCPServer)
	if !ok {
		return errors.New("unexpected type from GetAuthorizedObjects for MCPServers")
	}

	for _, s := range authorizedServers {
		log.Infof("Found MCP server: %s", s.Name)
		if err := a.addMCPServerBlocking(ctx, s); err != nil {
			return err
		}
	}

	return nil
}

// addMCPServerBlocking enqueues an MCPServer add and waits for the result.
// Returns nil for IgnoreErrors=true failures (logged and continued);
// surfaces non-ignored failures so the runtime can shut down gracefully via
// the runner manager, matching the components bootstrap behaviour.
func (a *DaprRuntime) addMCPServerBlocking(ctx context.Context, s mcpserverapi.MCPServer) error {
	ch := a.processor.AddPendingMCPServer(ctx, s)
	if ch == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-ch:
		if err == nil {
			return nil
		}
		err = fmt.Errorf("process MCPServer %s error: %s", s.Name, err)
		if s.Spec.IgnoreErrors {
			log.Errorf("Ignoring error processing MCPServer: %s", err)
			return nil
		}
		log.Warnf("Error processing MCPServer, daprd will exit gracefully: %s", err)
		return err
	}
}

func (a *DaprRuntime) flushOutstandingMCPServers(ctx context.Context) error {
	log.Info("Waiting for all outstanding MCP servers to be processed…")
	if err := a.processor.Flush(ctx); err != nil {
		return fmt.Errorf("flush MCP servers: %w", err)
	}
	log.Info("All outstanding MCP servers processed")
	return nil
}
