// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package channel

import (
	"context"

	"github.com/dapr/components-contrib/configuration"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

const (
	// DefaultChannelAddress is the address that user application listen to
	DefaultChannelAddress = "127.0.0.1"
)

// AppChannel is an abstraction over communications with user code
type AppChannel interface {
	GetBaseAddress() string
	GetAppConfig() (*config.ApplicationConfig, error)
	InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	OnConfigurationEvent(ctx context.Context, storeName string, appID string, items []*configuration.Item) error
}
