// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

const (
	strategyKey = "keyPrefix"

	strategyAppid     = "appid"
	strategyStoreName = "name"
	strategyNone      = "none"
	strategyDefault   = strategyAppid

	daprSeparator = "||"
)

var statesConfiguration = map[string]*StoreConfiguration{}

type StoreConfiguration struct {
	keyPrefixStrategy string
}

func SaveStateConfiguration(storeName string, metadata map[string]string) error {
	strategy := metadata[strategyKey]
	strategy = strings.ToLower(strategy)
	if strategy == "" {
		strategy = strategyDefault
	}
	if sc, ok := statesConfiguration[storeName]; ok {
		if sc.keyPrefixStrategy != strategy {
			return errors.Errorf("the storename occurs inconsistency, new: %s, old: %s", strategy, sc.keyPrefixStrategy)
		}
	}

	statesConfiguration[storeName] = &StoreConfiguration{keyPrefixStrategy: strategy}
	return nil
}

func GetModifiedStateKey(key, storeName, appID string) string {
	stateConfiguration := getStateConfiguration(storeName)
	switch stateConfiguration.keyPrefixStrategy {
	case strategyNone:
		return key
	case strategyStoreName:
		return fmt.Sprintf("%s%s%s", storeName, daprSeparator, key)
	case strategyAppid:
		if appID == "" {
			return key
		}
		return fmt.Sprintf("%s%s%s", appID, daprSeparator, key)
	default:
		return fmt.Sprintf("%s%s%s", stateConfiguration.keyPrefixStrategy, daprSeparator, key)
	}
}

func GetOriginalStateKey(modifiedStateKey string) string {
	splits := strings.Split(modifiedStateKey, daprSeparator)
	if len(splits) <= 1 {
		return modifiedStateKey
	}
	return splits[1]
}

func getStateConfiguration(storeName string) *StoreConfiguration {
	c := statesConfiguration[storeName]
	if c == nil {
		c = &StoreConfiguration{keyPrefixStrategy: strategyDefault}
		statesConfiguration[storeName] = c
	}

	return c
}
