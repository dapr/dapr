/*
Copyright 2022 The Dapr Authors
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
	"bufio"
	"bytes"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	// DefaultKubeClusterDomain is the default value of KubeClusterDomain.
	DefaultKubeClusterDomain = "cluster.local"
	defaultResolvPath        = "/etc/resolv.conf"
	commentMarker            = "#"
)

var searchRegexp = regexp.MustCompile(`^\s*search\s*(([^\s]+\s*)*)$`)

// GetKubeClusterDomain search KubeClusterDomain value from /etc/resolv.conf file.
func GetKubeClusterDomain() (string, error) {
	resolvContent, err := getResolvContent(defaultResolvPath)
	if err != nil {
		return "", err
	}
	return getClusterDomain(resolvContent)
}

func getClusterDomain(resolvConf []byte) (string, error) {
	var kubeClusterDomain string
	searchDomains := getResolvSearchDomains(resolvConf)
	sort.Strings(searchDomains)
	if len(searchDomains) == 0 || searchDomains[0] == "" {
		kubeClusterDomain = DefaultKubeClusterDomain
	} else {
		kubeClusterDomain = searchDomains[0]
	}
	return kubeClusterDomain, nil
}

func getResolvContent(resolvPath string) ([]byte, error) {
	return os.ReadFile(resolvPath)
}

func getResolvSearchDomains(resolvConf []byte) []string {
	var (
		domains []string
		lines   [][]byte
	)

	scanner := bufio.NewScanner(bytes.NewReader(resolvConf))
	for scanner.Scan() {
		line := scanner.Bytes()
		commentIndex := bytes.Index(line, []byte(commentMarker))
		if commentIndex == -1 {
			lines = append(lines, line)
		} else {
			lines = append(lines, line[:commentIndex])
		}
	}

	for _, line := range lines {
		match := searchRegexp.FindSubmatch(line)
		if match == nil {
			continue
		}
		domains = strings.Fields(string(match[1]))
	}

	return domains
}
