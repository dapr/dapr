/*
Copyright 2025 The Dapr Authors
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

package utils

import (
	"strings"

	diagConsts "github.com/dapr/dapr/pkg/diagnostics/consts"
	otelbaggage "go.opentelemetry.io/otel/baggage"
)

// ProcessBaggageValues handles baggage header validation, property parsing, and member creation.
func ProcessBaggageValues(baggageValues []string) ([]string, []otelbaggage.Member) {
	var validBaggage []string
	var members []otelbaggage.Member

	for _, baggageHeader := range baggageValues {
		baggageHeader = strings.TrimSpace(baggageHeader)
		if baggageHeader == "" {
			continue
		}

		// Split baggage values & validate each one
		items := strings.Split(baggageHeader, ",")
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if IsValidBaggage(item) {
				// For items with properties, we need to split only the key=value part
				parts := strings.SplitN(item, ";", 2)
				keyValue := strings.SplitN(parts[0], "=", 2)
				if len(keyValue) == 2 {
					if member, err := otelbaggage.NewMember(keyValue[0], keyValue[1]); err == nil {
						members = append(members, member)
						// Keep the entire item including properties
						validBaggage = append(validBaggage, item)
					}
				}
			}
		}
	}

	return validBaggage, members
}

// IsValidBaggage checks if the baggage header value is valid according to the W3C spec.
// A valid baggage header should be a comma-separated list of key-value pairs,
// where each key-value pair is separated by an equals sign.
// Each key-value pair can have optional properties in the format key=value;prop1=value1;prop2=value2.
// The key and value should not be empty.
// The total length should not exceed MaxBaggageLength.
func IsValidBaggage(baggage string) bool {
	if baggage == "" {
		return false
	}

	if len(baggage) > diagConsts.MaxBaggageLength {
		return false
	}

	pairs := strings.Split(baggage, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		// Split into kv and properties
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return false
		}
		key := strings.TrimSpace(parts[0])
		if key == "" || !isValidToken(key) {
			return false
		}

		// check value & properties
		valueAndProps := strings.TrimSpace(parts[1])
		if valueAndProps == "" {
			return false
		}

		valueParts := strings.Split(valueAndProps, ";")
		value := strings.TrimSpace(valueParts[0])
		if value == "" {
			return false
		}

		// check properties
		for i := 1; i < len(valueParts); i++ {
			prop := strings.TrimSpace(valueParts[i])
			if prop == "" {
				continue
			}
			propParts := strings.SplitN(prop, "=", 2)
			if len(propParts) != 2 {
				return false
			}
			propKey := strings.TrimSpace(propParts[0])
			propValue := strings.TrimSpace(propParts[1])
			if propKey == "" || propValue == "" || !isValidToken(propKey) {
				return false
			}
		}
	}
	return true
}

// isValidToken checks if a string is a valid token according to the W3C spec.
// A token can contain alphanumeric characters, hyphens, and underscores.
func isValidToken(s string) bool {
	for _, c := range s {
		if !isTokenChar(c) {
			return false
		}
	}
	return true
}

// isTokenChar checks if a character is valid in a token according to the W3C spec.
func isTokenChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' ||
		c == '_'
}
