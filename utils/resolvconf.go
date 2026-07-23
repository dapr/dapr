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
	"context"
	"errors"
	"net"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultKubeClusterDomain is the default value of KubeClusterDomain.
	DefaultKubeClusterDomain = "cluster.local"
	defaultResolvPath        = "/etc/resolv.conf"
	commentMarker            = "#"
)

var searchRegexp = regexp.MustCompile(`^\s*search\s*(([^\s]+\s*)*)$`)

func GetKubeClusterDomainFromDNS(ctx context.Context) (string, error) {
	const apiSvc = "kubernetes.default.svc"

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	r := net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
	}
	cname, err := r.LookupCNAME(ctx, apiSvc)
	if err != nil {
		return "", err
	}

	clusterDomain := clusterDomainFromCNAME(apiSvc, cname)
	if clusterDomain == "" {
		return "", errors.New("could not parse cluster domain from CNAME: " + cname)
	}

	return clusterDomain, nil
}

// clusterDomainFromCNAME extracts the cluster domain from a CNAME response.
// DNS CNAME responses typically include a trailing dot (e.g.
// "kubernetes.default.svc.cluster.local."), which must be stripped.
func clusterDomainFromCNAME(apiSvc, cname string) string {
	clusterDomain := strings.TrimPrefix(cname, apiSvc)
	clusterDomain = strings.Trim(clusterDomain, ".")
	return clusterDomain
}

// GetKubeClusterDomain search KubeClusterDomain value from /etc/resolv.conf file.
func GetKubeClusterDomain() (string, error) {
	resolvContent, err := getResolvContent(defaultResolvPath)
	if err != nil {
		return "", err
	}
	return getClusterDomain(resolvContent)
}

func getClusterDomain(resolvConf []byte) (string, error) {
	searchDomains := getResolvSearchDomains(resolvConf)
	if clusterDomain := clusterDomainFromSearchDomains(searchDomains); clusterDomain != "" {
		return clusterDomain, nil
	}
	// No Kubernetes-shaped "svc.<cluster-domain>" search entry was found; fall
	// back to the default cluster domain.
	return DefaultKubeClusterDomain, nil
}

// clusterDomainFromSearchDomains derives the Kubernetes cluster domain from the
// resolv.conf search list. A pod's search list has the shape
// "<namespace>.svc.<cluster-domain> svc.<cluster-domain> <cluster-domain>", so
// the cluster domain is the "svc.<cluster-domain>" entry with its leading
// "svc." label removed. Matching on that label, rather than sorting the entries
// or assuming a fixed number of namespace labels, keeps detection independent
// of the namespace name, preserves the resolver's search order, supports custom
// cluster domains, and ignores unrelated search entries. It returns "" when no
// such entry exists.
func clusterDomainFromSearchDomains(searchDomains []string) string {
	const svcLabel = "svc."
	for _, domain := range searchDomains {
		// resolv.conf entries may be written with a trailing dot.
		domain = strings.Trim(domain, ".")
		// The namespace-qualified entry ("<namespace>.svc.<cluster-domain>")
		// does not begin with "svc.", so only "svc.<cluster-domain>" matches.
		if clusterDomain, ok := strings.CutPrefix(domain, svcLabel); ok && clusterDomain != "" {
			return clusterDomain
		}
	}
	return ""
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
		before, _, ok := bytes.Cut(line, []byte(commentMarker))
		if !ok {
			lines = append(lines, line)
		} else {
			lines = append(lines, before)
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
