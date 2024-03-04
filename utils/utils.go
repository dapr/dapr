/*
Copyright 2021 The Dapr Authors
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
	"fmt"
	"io/fs"
	"os"
	"strings"
)

const (
	DotDelimiter = "."
)

// Contains reports whether v is present in s.
// Similar to https://pkg.go.dev/golang.org/x/exp/slices#Contains.
func Contains[T comparable](s []T, v T) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// ContainsPrefixed reports whether v is prefixed by any of the strings in s.
func ContainsPrefixed(prefixes []string, v string) bool {
	for _, e := range prefixes {
		if strings.HasPrefix(v, e) {
			return true
		}
	}
	return false
}

// SetEnvVariables set variables to environment.
func SetEnvVariables(variables map[string]string) error {
	for key, value := range variables {
		err := os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetEnvOrElse get the value from the OS environment or use the else value if variable is not present.
func GetEnvOrElse(name, orElse string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return orElse
}

// GetIntValOrDefault returns an int value if greater than 0 OR default value.
func GetIntValOrDefault(val int, defaultValue int) int {
	if val > 0 {
		return val
	}
	return defaultValue
}

// IsSocket returns if the given file is a unix socket.
func IsSocket(f fs.FileInfo) bool {
	return f.Mode()&fs.ModeSocket != 0
}

// SocketExists returns true if the file in that path is an unix socket.
func SocketExists(socketPath string) bool {
	if s, err := os.Stat(socketPath); err == nil {
		return IsSocket(s)
	}
	return false
}

func PopulateMetadataForBulkPublishEntry(reqMeta, entryMeta map[string]string) map[string]string {
	resMeta := map[string]string{}
	for k, v := range entryMeta {
		resMeta[k] = v
	}
	for k, v := range reqMeta {
		if _, ok := resMeta[k]; !ok {
			// Populate only metadata key that is already not present in the entry level metadata map
			resMeta[k] = v
		}
	}

	return resMeta
}

// Filter returns a new slice containing all items in the given slice that satisfy the given test.
func Filter[T any](items []T, test func(item T) bool) []T {
	filteredItems := make([]T, len(items))
	n := 0
	for i := 0; i < len(items); i++ {
		if test(items[i]) {
			filteredItems[n] = items[i]
			n++
		}
	}
	return filteredItems[:n]
}

// MapToSlice is the inversion of SliceToMap. Order is not guaranteed as map retrieval order is not.
func MapToSlice[T comparable, V any](m map[T]V) []T {
	l := make([]T, len(m))
	var i int
	for uid := range m {
		l[i] = uid
		i++
	}
	return l
}

const (
	logNameFmt        = "%s (%s)"
	logNameVersionFmt = "%s (%s/%s)"
)

// ComponentLogName returns the name of a component that can be used in logging.
func ComponentLogName(name, typ, version string) string {
	if version == "" {
		return fmt.Sprintf(logNameFmt, name, typ)
	}

	return fmt.Sprintf(logNameVersionFmt, name, typ, version)
}

// GetNamespaceOrDefault returns the namespace for Dapr, or the default namespace if it is not set.
func GetNamespaceOrDefault(defaultNamespace string) string {
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = defaultNamespace
	}
	return namespace
}

func ParseServiceAddr(val string) []string {
	p := strings.Split(val, ",")
	for i, v := range p {
		p[i] = strings.TrimSpace(v)
	}
	return p
}
