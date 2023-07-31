/*
Copyright 2023 The Dapr Authors
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

package namespacednamematcher

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/dapr/dapr/utils"
)

// sort by length and if needed by regular comparison
type shortestPrefixSortedSlice []string

func (s shortestPrefixSortedSlice) Less(i, j int) bool { return len(s[i]) < len(s[j]) || s[i] < s[j] }
func (s shortestPrefixSortedSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s shortestPrefixSortedSlice) Len() int           { return len(s) }

type equalPrefixLists struct {
	equal  []string
	prefix []string
}

type EqualPrefixNameNamespaceMatcher struct {
	prefixed       map[string]*equalPrefixLists
	equal          map[string]*equalPrefixLists
	sortedPrefixes []string
}

// CreateFromString creates two maps from the CSV provided by the user of ns:sa values,
// one with namespace prefixes and one with namespace exact values.
// Inside each map we can have exact name or prefixed names.
// Note there might be overlap in prefixes, but we are not filtering it for now.
func CreateFromString(s string) (*EqualPrefixNameNamespaceMatcher, error) {
	matcher := &EqualPrefixNameNamespaceMatcher{}
	for _, nameNamespace := range strings.Split(s, ",") {
		saNs := strings.Split(nameNamespace, ":")
		if len(saNs) != 2 {
			return nil, errors.New("service account namespace pair not following expected format 'namespace:serviceaccountname'")
		}
		ns := strings.TrimSpace(saNs[0])
		sa := strings.TrimSpace(saNs[1])

		if len(ns) == 0 && len(sa) == 0 {
			return nil, errors.New("service account name and namespace cannot both be empty")
		}
		nsPrefix, prefixFound, err := getPrefix(ns)
		if err != nil {
			return nil, err
		}
		if prefixFound {
			if matcher.prefixed == nil {
				matcher.prefixed = make(map[string]*equalPrefixLists)
			}
			if _, ok := matcher.prefixed[nsPrefix]; !ok {
				matcher.prefixed[nsPrefix] = &equalPrefixLists{}
			}
			saPrefix, saPrefixFound, prefixErr := getSaEqualPrefix(sa, matcher.prefixed[nsPrefix])
			if prefixErr != nil {
				return nil, prefixErr
			}
			if saPrefixFound && saPrefix == "" && nsPrefix == "" {
				return nil, errors.New("service account name and namespace prefixes cannot both be empty (ie '*:*'")
			}
		} else {
			if matcher.equal == nil {
				matcher.equal = make(map[string]*equalPrefixLists)
			}
			if _, ok := matcher.equal[ns]; !ok {
				matcher.equal[ns] = &equalPrefixLists{}
			}
			if _, _, prefixErr := getSaEqualPrefix(sa, matcher.equal[ns]); prefixErr != nil {
				return nil, prefixErr
			}
		}
	}
	if len(matcher.prefixed) != 0 {
		matcher.sortedPrefixes = utils.MapToSlice(matcher.prefixed)
		sort.Sort(shortestPrefixSortedSlice(matcher.sortedPrefixes))
	}
	return matcher, nil
}

func getSaEqualPrefix(sa string, namespaceNames *equalPrefixLists) (saPrefix string, saPrefixFound bool, err error) {
	saPrefix, saPrefixFound, err = getPrefix(sa)
	if err != nil {
		return "", false, err
	}
	if saPrefixFound {
		if !utils.Contains(namespaceNames.prefix, saPrefix) {
			namespaceNames.prefix = append(namespaceNames.prefix, saPrefix)
		}
	} else if !utils.Contains(namespaceNames.equal, sa) {
		namespaceNames.equal = append(namespaceNames.equal, sa)
	}
	// sort prefixes by length/value
	sort.Sort(shortestPrefixSortedSlice(namespaceNames.prefix))
	return saPrefix, saPrefixFound, nil
}

func getPrefix(s string) (string, bool, error) {
	wildcardIndex := strings.Index(s, "*")
	if wildcardIndex == -1 {
		return "", false, nil
	}
	if wildcardIndex != (len(s) - 1) {
		return "", false, fmt.Errorf("we only allow a single wildcard at the end of the string to indicate prefix matching for allowed servicename or namespace, and we were provided with: %s", s)
	}
	return s[:len(s)-1], true, nil
}

// MatchesNamespacedName matches an object against all the potential options in this matcher, we first check sorted prefixes
// to try to match the shortest one that could potentially cover others
func (m *EqualPrefixNameNamespaceMatcher) MatchesNamespacedName(namespace, name string) bool {
	for _, nsPrefix := range m.sortedPrefixes {
		if strings.HasPrefix(namespace, nsPrefix) {
			return utils.ContainsPrefixed(m.prefixed[nsPrefix].prefix, name) || utils.Contains(m.prefixed[nsPrefix].equal, name)
		}
	}
	for ns, values := range m.equal {
		if ns == namespace {
			return utils.ContainsPrefixed(values.prefix, name) || utils.Contains(values.equal, name)
		}
	}
	return false
}
