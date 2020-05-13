package http

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSelectorRule(t *testing.T) {
	testCases := []struct {
		expression string
		rule selectorRule
	}{
		{
			expression: "path=test,test1,test2;method=get,post,delete;version=v1.0",
		  rule: selectorRule{
				Paths: []string{
					"test",
					"test1",
					"test2",
				},
				Methods: []string{
					"get",
					"post",
					"delete",
				},
				Versions: []string{
					"v1.0",
				},
			},
		},
		{
			expression: "method=get;path=test,test1;",
		  rule: selectorRule{
				Paths: []string{
					"test",
					"test1",
				},
				Methods: []string{
					"get",
				},
				Versions: nil,
			},
		},
		{
			expression: "version=v1.0,v2.0;method=get,post,options;",
		  rule: selectorRule{
				Paths: nil,
				Methods: []string{
					"get",
					"post",
					"options",
				},
				Versions: []string{
					"v1.0",
					"v2.0",
				},
			},
		},
		{
			expression: "method=get;path=test,test1;",
		  rule: selectorRule{
				Paths: []string{
					"test",
					"test1",
				},
				Methods: []string{
					"get",
				},
				Versions: nil,
			},
		},
		{
			expression: "path=test1",
		  rule: selectorRule{
				Paths: []string{
					"test1",
				},
				Methods: nil,
				Versions: nil,
			},
		},
		{
			expression: "method=get,post,path=badexp",
		  rule: selectorRule{
				Paths: nil,
				Methods: nil,
				Versions: nil,
			},
		},
		{
			expression: "path;=method=version;=get",
		  rule: selectorRule{
				Paths: nil,
				Methods: nil,
				Versions: nil,
			},
		},
		{
			expression: "",
		  rule: selectorRule{
				Paths: nil,
				Methods: nil,
				Versions: nil,
			},
		},
		{
			expression: "12345",
		  rule: selectorRule{
				Paths: nil,
				Methods: nil,
				Versions: nil,
			},
		},
		{
			expression: "path= test1;method=get, post",
		  rule: selectorRule{
				Paths: []string{
					"test1",
				},
				Methods: []string{
					"get",
					"post",
				},
				Versions: nil,
			},
		},
		{
			expression: "path= ;method=, post",
		  rule: selectorRule{
				Paths: nil,
				Methods: []string{
					"post",
				},
				Versions: nil,
			},
		},
	}

	for i, tt := range testCases {
		testName := fmt.Sprintf("test%d", i)
		t.Run(testName, func(t *testing.T) {
			newRule := newSelectorRule(tt.expression)
			assert.True(t, reflect.DeepEqual(tt.rule, *newRule))
		})
	}
}

func TestMatchSelector(t *testing.T) {
	testCases := []struct {
		path string
		version string
		method string
		selector map[string]string
		match bool
	}{
		{
			path: "test1",
			version: "v1.0",
			method: "get",
			selector: map[string]string{
				"testRule1": "path=test1;version=v1.0;method=get",
			},
			match: true,
		},
		{
			path: "test1/test2/test3",
			version: "v1.0",
			method: "get",
			selector: map[string]string{
				"testRule1": "path=test1;version=v1.0;method=get",
			},
			match: true,
		},
		{
			path: "test1/test2/test3",
			version: "v1.0",
			method: "get",
			selector: map[string]string{
				"testRule1": "path=test1/test2;version=v1.0;method=get",
			},
			match: true,
		},
		{
			path: "test1/myapp",
			version: "v1.0",
			method: "post",
			selector: map[string]string{
				"testRule1": "path=test1/myapp;version=v1.0;method=get,post",
			},
			match: true,
		},
		{
			path: "test1/myapp",
			version: "v1.0",
			method: "post",
			selector: map[string]string{
				"testRule1": "path=test1/myapp;version=v2.0;method=get,post",
			},
			match: false,
		},
		{
			path: "test1/myapp",
			version: "v1.0",
			method: "post",
			selector: map[string]string{
				"testRule1": "path=test1/myapp;version=v2.0;method=get,post",
				"testRule2": "path=test1;version=v1.0;method=post",
			},
			match: true,
		},
		{
			path: "test1/myapp",
			version: "v1.0",
			method: "delete",
			selector: map[string]string{
				"testRule1": "path=test2;version=v1.0;method=options,get,post,delete",
				"testRule2": "path=test3;version=v1.0;method=delete",
			},
			match: false,
		},
		{
			path: "test1/myapp",
			version: "v1.0",
			method: "delete",
			selector: map[string]string{
				"testRule1": "path=test2;version=v1.0;method=options,get,post,delete",
				"testRule2": "path=test3;version=v1.0;method=delete",
				"testRule3": "path=test1;version=v1.0;method=delete",
			},
			match: true,
		},
	}

	for i, tt := range testCases {
		testName := fmt.Sprintf("test%d", i)
		t.Run(testName, func(t *testing.T) {
			matched := matchSelector(tt.path, tt.version, tt.method, tt.selector)
			assert.Equal(t, tt.match, matched)
		})
	}
}