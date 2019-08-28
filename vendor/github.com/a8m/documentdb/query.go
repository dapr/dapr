package documentdb

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type P = Parameter

type Query struct {
	Query      string      `json:"query"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

func NewQuery(query string, parameters ...Parameter) *Query {
	return &Query{query, parameters}
}
