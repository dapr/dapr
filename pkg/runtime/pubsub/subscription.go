package pubsub

type Subscription struct {
	PubsubName   string            `json:"pubsubname"`
	Topic        string            `json:"topic"`
	Metadata     map[string]string `json:"metadata"`
	Rules        []*Rule           `json:"rules,omitempty"`
	Scopes       []string          `json:"scopes"`
	PipelineSpec PipelineSpec      `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
}

type Rule struct {
	Match Expr   `json:"match"`
	Path  string `json:"path"`
}

type Expr interface {
	Eval(variables map[string]interface{}) (interface{}, error)
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers" yaml:"handlers"`
}
type HandlerSpec struct {
	Name    string `json:"name" yaml:"name"`
	Type    string `json:"type" yaml:"type"`
	Version string `json:"version" yaml:"version"`
}
