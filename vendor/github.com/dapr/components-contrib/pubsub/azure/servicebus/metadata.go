// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicebus

// Reference for settings:
// https://github.com/Azure/azure-service-bus-go/blob/54b2faa53e5216616e59725281be692acc120c34/subscription_manager.go#L101
type metadata struct {
	ConnectionString              string `json:"connectionString"`
	ConsumerID                    string `json:"consumerID"`
	TimeoutInSec                  int    `json:"timeoutInSec"`
	DisableEntityManagement       bool   `json:"disableEntityManagement"`
	MaxDeliveryCount              *int   `json:"maxDeliveryCount"`
	LockDurationInSec             *int   `json:"lockDurationInSec"`
	DefaultMessageTimeToLiveInSec *int   `json:"defaultMessageTimeToLiveInSec"`
	AutoDeleteOnIdleInSec         *int   `json:"autoDeleteOnIdleInSec"`
}
