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
