// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package retry

import "time"

const (
	DefaultLinearBackoffInterval = time.Second
	DefaultLinearRetryCount      = 3
)
