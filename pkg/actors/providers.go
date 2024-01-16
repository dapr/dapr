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

package actors

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
)

type (
	placementProviderFactory = func(opts internal.ActorsProviderOptions) internal.PlacementService
	remindersProviderFactory = func(opts internal.ActorsProviderOptions) internal.RemindersProvider
)

var (
	placementProviders map[string]func(config Config) (placementProviderFactory, error) = map[string]func(config Config) (placementProviderFactory, error){
		"placement": func(config Config) (func(opts internal.ActorsProviderOptions) internal.PlacementService, error) {
			return placement.NewActorPlacement, nil
		},
	}
	remindersProviders map[string]func(config Config, placement internal.PlacementService) (remindersProviderFactory, error) = map[string]func(config Config, placement internal.PlacementService) (remindersProviderFactory, error){
		"default": func(config Config, placement internal.PlacementService) (func(opts internal.ActorsProviderOptions) internal.RemindersProvider, error) {
			return reminders.NewRemindersProvider, nil
		},
	}
)

// GetPlacementProvider returns the factory method for the configured placement provider
func (c Config) GetPlacementProvider() (placementProviderFactory, error) {
	// If ActorsService is empty, use the default implementation
	service := "placement"
	if c.ActorsService != "" {
		// Get the name of the provider from the prefix of the ActorsService configuration option
		idx := strings.IndexRune(c.ActorsService, ':')
		if idx <= 0 {
			return nil, errors.New("invalid value for the actors service configuration: does not contain the name of the service")
		}

		service = c.ActorsService[:idx]
	}

	factory, ok := placementProviders[service]
	if !ok {
		return nil, fmt.Errorf("unsupported actor service provider '%s'", service)
	}

	log.Infof("Configuring actors placement provider '%s'. Configuration: '%s'", service, c.ActorsService)

	return factory(c)
}

// GetRemindersProvider returns the factory method for the configured reminders provider
func (c Config) GetRemindersProvider(placement internal.PlacementService) (remindersProviderFactory, error) {
	// If RemindersService is empty, use the default implementation
	service := "default"
	if c.RemindersService != "" && c.RemindersService != "default" {
		// Get the name of the provider from the prefix of the RemindersService configuration option
		idx := strings.IndexRune(c.RemindersService, ':')
		if idx <= 0 {
			return nil, errors.New("invalid value for the reminders service configuration: does not contain the name of the service")
		}

		service = c.RemindersService[:idx]
	}

	factory, ok := remindersProviders[service]
	if !ok {
		return nil, fmt.Errorf("unsupported reminder service provider '%s'", service)
	}

	log.Infof("Configuring actor reminders provider '%s'. Configuration: '%s'", service, c.RemindersService)

	return factory(c, placement)
}
