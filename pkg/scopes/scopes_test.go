package scopes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAllowedTopics(t *testing.T) {
	allowedTests := []struct {
		Metadata map[string]string
		Target   []string
		Msg      string
	}{
		{
			Metadata: nil,
			Target:   []string{},
			Msg:      "pass",
		},
		{
			Metadata: map[string]string{
				"allowedTopics": "topic1,topic2,topic3",
			},
			Target: []string{"topic1", "topic2", "topic3"},
			Msg:    "pass",
		},
		{
			Metadata: map[string]string{
				"allowedTopics": "topic1, topic2, topic3",
			},
			Target: []string{"topic1", "topic2", "topic3"},
			Msg:    "pass, include whitespace",
		},
		{
			Metadata: map[string]string{
				"allowedTopics": "",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Metadata: map[string]string{
				"allowedTopics": "topic1, topic1, topic1",
			},
			Target: []string{"topic1"},
			Msg:    "pass, include whitespace and repeated topic",
		},
	}
	for _, item := range allowedTests {
		assert.Equal(t, GetAllowedTopics(item.Metadata), item.Target)
	}
}

func TestGetScopedTopics(t *testing.T) {
	scopedTests := []struct {
		Scope    string
		AppID    string
		Metadata map[string]string
		Target   []string
		Msg      string
	}{
		{
			Scope:    "subscriptionScopes",
			AppID:    "appid1",
			Metadata: map[string]string{},
			Target:   []string{},
			Msg:      "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid2=topic1",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1=topic1",
			},
			Target: []string{"topic1"},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1=topic1, topic2",
			},
			Target: []string{"topic1", "topic2"},
			Msg:    "pass, include whitespace",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1=topic1;appid1=topic2",
			},
			Target: []string{"topic1", "topic2"},
			Msg:    "pass, include repeated appid",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1=topic1;appid1=topic1",
			},
			Target: []string{"topic1"},
			Msg:    "pass, include repeated appid and topic",
		},
	}
	for _, item := range scopedTests {
		assert.Equal(t,
			GetScopedTopics(item.Scope, item.AppID, item.Metadata),
			item.Target)
	}
}
