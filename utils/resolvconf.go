package utils

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"regexp"
	"sort"
	"strings"
)

const (
	DefaultKubeClusterDomain = "cluster.local"
	defaultResolvPath        = "/etc/resolv.conf"
	commentMarker            = "#"
)

var (
	searchRegexp = regexp.MustCompile(`^\s*search\s*(([^\s]+\s*)*)$`)
)

func GetKubeClusterDomain() (string, error) {
	resolvContent, err := GetResolvContent(defaultResolvPath)
	if err != nil {
		return "", err
	}
	return getClusterDomain(resolvContent)
}

func getClusterDomain(resolvConf []byte) (string, error) {
	var kubeClusterDomian string
	searchDomains := GetResolvSearchDomains(resolvConf)
	if len(searchDomains) == 0 {
		kubeClusterDomian = DefaultKubeClusterDomain
	} else {
		sort.Strings(searchDomains)
		kubeClusterDomian = searchDomains[0]
	}
	return kubeClusterDomian, nil
}

func GetResolvContent(resolvPath string) ([]byte, error) {
	return ioutil.ReadFile(resolvPath)
}

func GetResolvSearchDomains(resolvConf []byte) []string {
	var (
		domains []string
		lines   [][]byte
	)

	scanner := bufio.NewScanner(bytes.NewReader(resolvConf))
	for scanner.Scan() {
		line := scanner.Bytes()
		var commentIndex = bytes.Index(line, []byte(commentMarker))
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
