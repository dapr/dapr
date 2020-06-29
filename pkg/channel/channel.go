// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package channel

import (
	"context"
	"time"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

const (
	// DefaultChannelAddress is the address that user application listen to
	DefaultChannelAddress = "127.0.0.1"

	// DefaultChannelRequestTimeout is the timeout when Dapr calls user app via app channel
	DefaultChannelRequestTimeout = time.Minute * 1
)

// AppChannel is an abstraction over communications with user code
type AppChannel interface {
	GetBaseAddress() string
	InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
}
