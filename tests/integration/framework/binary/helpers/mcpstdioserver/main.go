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

// mcpstdioserver is a minimal MCP server that communicates over stdin/stdout,
// used by the integration tests to exercise the MCP stdio transport.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stdio-test", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stdio_echo",
		Description: "Echoes a message via stdio",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "stdio-pong"}},
		}, struct{}{}, nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		os.Exit(1)
	}
}
