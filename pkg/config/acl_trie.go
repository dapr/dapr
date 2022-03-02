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

package config

import "strings"

const (
	SingleStageWildcard = "/*"
	MultiStageWildcard  = "/**"
	Separation          = "/"
)

type Trie struct {
	root *trieNode
}

type trieNode struct {
	Char     string
	Data     *AccessControlListOperationAction
	SubNodes []*trieNode
}

func NewTrie() *Trie {
	return &Trie{
		root: newTrieNode("/", nil),
	}
}

func newTrieNode(char string, data *AccessControlListOperationAction) *trieNode {
	node := &trieNode{
		Char:     char,
		Data:     data,
		SubNodes: nil,
	}

	return node
}

func (trie *Trie) Search(operation string) *AccessControlListOperationAction {
	node := trie.root

	operationParts := strings.Split(operation, Separation)

	length := len(operationParts)
	for index, char := range operationParts {
		if index == 0 {
			continue
		}
		isEnd := index == length-1
		char = Separation + char
		node = node.findSubNode(char, isEnd)
		if node == nil {
			return nil
		}

		if node.Data != nil {
			if !isEnd && strings.HasSuffix(node.Char, SingleStageWildcard) && !strings.HasSuffix(node.Char, MultiStageWildcard) {
				continue
			}
			return node.Data
		} else if isEnd {
			node = node.findSubNode(SingleStageWildcard, isEnd)
			if node != nil && node.Data != nil {
				return node.Data
			}
		}
	}
	return nil
}

func (node *trieNode) findSubNode(target string, isEnd bool) *trieNode {
	if nil == node.SubNodes {
		return nil
	}

	return findNodeWithWildcard(target, node.SubNodes, isEnd)
}

func (trie *Trie) PutOperationAction(operation string, data *AccessControlListOperationAction) {
	operationParts := strings.Split(operation, Separation)
	length := len(operationParts)

	node := trie.root
	for index, char := range operationParts {
		if index == 0 {
			continue
		}
		char = Separation + char

		subNode := findNode(char, node.SubNodes)
		var newNode *trieNode
		if nil == subNode {
			if index == length-1 {
				newNode = newTrieNode(char, data)
			} else {
				newNode = newTrieNode(char, nil)
			}

			node.addSubNode(newNode)
			node = newNode
		} else if index == length-1 {
			if subNode.Data == nil {
				subNode.Data = data
			}
		} else {
			node = subNode
		}
	}
}

func findNodeWithWildcard(char string, nodes []*trieNode, isEnd bool) *trieNode {
	if nil == nodes || len(nodes) < 1 {
		return nil
	}

	for _, node := range nodes {
		if node.Char == char {
			return node
		}

		if node.Char == SingleStageWildcard {
			if isEnd {
				return node
			}
			continue
		}
		if node.Char == MultiStageWildcard {
			return node
		}

		if isMatch(char, node.Char) {
			return node
		}
	}

	return nil
}

func findNode(char string, nodes []*trieNode) *trieNode {
	if nil == nodes || len(nodes) < 1 {
		return nil
	}

	for _, node := range nodes {
		if node.Char == char {
			return node
		}
	}

	return nil
}

func (node *trieNode) addSubNode(newNode *trieNode) {
	if nil == node.SubNodes {
		node.SubNodes = []*trieNode{newNode}
	} else {
		node.SubNodes = append(node.SubNodes, newNode)
	}
}

// Ability to provide '*' wildcard matching
// '*' can match any string, can be empty, i.e. match zero or any number of characters.
func isMatch(target string, patten string) bool {
	tl := len(target)
	pl := len(patten)

	matchResults := make([][]bool, tl+1)
	for i := 0; i <= tl; i++ {
		matchResults[i] = make([]bool, pl+1)
	}
	matchResults[0][0] = true
	for i := 1; i <= pl; i++ {
		if patten[i-1] == '*' {
			matchResults[0][i] = true
		} else {
			break
		}
	}
	for i := 1; i <= tl; i++ {
		for j := 1; j <= pl; j++ {
			if patten[j-1] == '*' {
				matchResults[i][j] = matchResults[i][j-1] || matchResults[i-1][j]
			} else if target[i-1] == patten[j-1] {
				matchResults[i][j] = matchResults[i-1][j-1]
			}
		}
	}
	return matchResults[tl][pl]
}
