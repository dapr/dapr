/*
Copyright 2024 The Dapr Authors
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
package apphealth

import "time"

const (
	// reportMinInterval is the minimum interval between health reports.
	reportMinInterval = time.Second
)

type Status struct {
	IsHealthy bool    `json:"ishealthy"`
	TimeUnix  int64   `json:"timeUnix"`
	Reason    *string `json:"reason,omitempty"`
}

// NewStatus returns a default status for the app.
// This defaults to healthy so subscriptions can start right away,
// because if there's no health check, then we mark the app as healthy right away so subscriptions can start.
func NewStatus(isHealthy bool, reason *string) *Status {
	return &Status{
		IsHealthy: isHealthy,
		TimeUnix:  time.Now().Unix(),
		Reason:    reason,
	}
}
