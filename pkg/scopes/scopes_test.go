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
				"allowedTopics": "topic1, topic1, topic1", //nolint:dupword
			},
			Target: []string{"topic1"},
			Msg:    "pass, include whitespace and repeated topic",
		},
		{
			Metadata: map[string]string{
				"Allowedtopics": "topic1",
			},
			Target: []string{"topic1"},
			Msg:    "pass, case-insensitive match",
		},
	}
	for _, item := range allowedTests {
		assert.Equal(t, GetAllowedTopics(item.Metadata), item.Target)
	}
}

func TestGetProtectedTopics(t *testing.T) {
	protectedTests := []struct {
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
			Metadata: map[string]string{},
			Target:   []string{},
			Msg:      "pass",
		},
		{
			Metadata: map[string]string{
				"protectedTopics": "",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Metadata: map[string]string{
				"protectedTopics": "topic1,topic2,topic3",
			},
			Target: []string{"topic1", "topic2", "topic3"},
			Msg:    "pass",
		},
		{
			Metadata: map[string]string{
				"protectedTopics": "topic1, topic2, topic3",
			},
			Target: []string{"topic1", "topic2", "topic3"},
			Msg:    "pass, include whitespace",
		},
		{
			Metadata: map[string]string{
				"protectedTopics": "topic1, topic1, topic1", //nolint:dupword
			},
			Target: []string{"topic1"},
			Msg:    "pass, include whitespace and repeated topic",
		},
		{
			Metadata: map[string]string{
				"protectedTOPICS": "topic1",
			},
			Target: []string{"topic1"},
			Msg:    "pass, case-insensitive match",
		},
	}
	for _, item := range protectedTests {
		assert.Equal(t, GetProtectedTopics(item.Metadata), item.Target)
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
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid2",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1;appid2=topic2",
			},
			Target: []string{},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionScopes",
			AppID: "appid1",
			Metadata: map[string]string{
				"subscriptionScopes": "appid1=topic1;appid2",
			},
			Target: []string{"topic1"},
			Msg:    "pass",
		},
		{
			Scope: "subscriptionSCOPES",
			AppID: "appid1",
			Metadata: map[string]string{
				"Subscriptionscopes": "appid1=topic1;appid2",
			},
			Target: []string{"topic1"},
			Msg:    "pass, case-insensitive match",
		},
	}
	for _, item := range scopedTests {
		assert.Equal(t,
			GetScopedTopics(item.Scope, item.AppID, item.Metadata),
			item.Target)
	}
}

func TestTopicMetadataWhitespace(t *testing.T) {
	parsers := []struct {
		name  string
		parse func(string) []string
	}{
		{
			name: "allowed",
			parse: func(value string) []string {
				return GetAllowedTopics(map[string]string{AllowedTopics: value})
			},
		},
		{
			name: "protected",
			parse: func(value string) []string {
				return GetProtectedTopics(map[string]string{ProtectedTopics: value})
			},
		},
		{
			name: "subscription",
			parse: func(value string) []string {
				return GetScopedTopics(SubscriptionScopes, "app2", map[string]string{
					SubscriptionScopes: "app1=other;\n\t app2 \r\n= " + value + ";app3=other",
				})
			},
		},
		{
			name: "publishing",
			parse: func(value string) []string {
				return GetScopedTopics(PublishingScopes, "app2", map[string]string{
					PublishingScopes: "app1=other;\n\t app2 \r\n= " + value + ";app3=other",
				})
			},
		},
	}
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "empty", value: "", want: []string{}},
		{name: "spaces_only", value: "   ", want: []string{}},
		{name: "whitespace_only", value: " \t\r\n\u00a0\u2003 ", want: []string{}},
		{name: "empty_entries", value: ",, ,\t,\n,", want: []string{}},
		{name: "trailing_and_consecutive_commas", value: ",topic1,, topic2, ", want: []string{"topic1", "topic2"}},
		{name: "trim_whitespace", value: "\t topic1 \r\n,\n\ttopic2\u2003,\u00a0topic3\t", want: []string{"topic1", "topic2", "topic3"}},
		{name: "deduplicate_after_trimming", value: "topic2,\ttopic1,\ntopic2,topic1\u2003", want: []string{"topic2", "topic1"}},
		{name: "legacy_embedded_spaces", value: "top ic1,topic 2", want: []string{"topic1", "topic2"}},
	}
	for _, parser := range parsers {
		t.Run(parser.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					assert.Equal(t, tt.want, parser.parse(tt.value))
				})
			}
		})
	}

	for _, scope := range []string{SubscriptionScopes, PublishingScopes} {
		t.Run(scope+"_empty_topics", func(t *testing.T) {
			assert.Equal(t, []string{}, GetScopedTopics(scope, "app2", map[string]string{
				scope: "app1=other;app2=,, ,;app3=other",
			}))
		})
		t.Run(scope, func(t *testing.T) {
			topics := GetScopedTopics(scope, "app2", map[string]string{
				scope: "invalid;app1=other;\n\tapp2 = ;\napp2= topic1,;\napp2=\ttopic1,topic2\n;app3=other",
			})
			assert.Equal(t, []string{"topic1", "topic2"}, topics)
		})
	}
}
