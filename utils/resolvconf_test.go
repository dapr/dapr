package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetClusterDomain(t *testing.T) {
	testCases := []struct {
		content  string
		expected string
	}{
		{
			content:  "search svc.cluster.local #test comment",
			expected: "svc.cluster.local",
		},
		{
			content:  "search default.svc.cluster.local svc.cluster.local cluster.local",
			expected: "cluster.local",
		},
		{
			content:  "",
			expected: "cluster.local",
		},
	}
	for _, tc := range testCases {
		domain, err := getClusterDomain([]byte(tc.content))
		if err != nil {
			t.Fatalf("get kube cluster domain error:%s", err)
		}
		assert.Equal(t, domain, tc.expected)
	}
}

func TestGetSearchDomains(t *testing.T) {
	testCases := []struct {
		content  string
		expected []string
	}{
		{
			content:  "search svc.cluster.local #test comment",
			expected: []string{"svc.cluster.local"},
		},
		{
			content:  "search default.svc.cluster.local svc.cluster.local cluster.local",
			expected: []string{"default.svc.cluster.local", "svc.cluster.local", "cluster.local"},
		},
		{
			content:  "",
			expected: []string{},
		},
	}
	for _, tc := range testCases {
		domains := getResolvSearchDomains([]byte(tc.content))
		assert.ElementsMatch(t, domains, tc.expected)
	}
}
