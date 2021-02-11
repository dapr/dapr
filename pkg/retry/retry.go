// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package retry

import "time"

const (
	DefaultLinearBackoffInterval = time.Second
	DefaultLinearRetryCount      = 3
)
