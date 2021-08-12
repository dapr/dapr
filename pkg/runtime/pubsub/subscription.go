package pubsub

type Subscription struct {
	PubsubName   string            `json:"pubsubname"`
	Topic        string            `json:"topic"`
	Route        string            `json:"route"`
	Metadata     map[string]string `json:"metadata"`
	Scopes       []string          `json:"scopes"`
	PipelineSpec PipelineSpec      `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers" yaml:"handlers"`
}
type HandlerSpec struct {
	Name    string `json:"name" yaml:"name"`
	Type    string `json:"type" yaml:"type"`
	Version string `json:"version" yaml:"version"`
}
