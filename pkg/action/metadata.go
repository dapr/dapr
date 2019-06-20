package action

type ActionMetadata struct {
	ID              string          `json:"id"`
	Actors          []ActorMetadata `json:"actors"`
	Protocol        string          `json:"protocol"`
	StateStore      string          `json:"stateStore"`
	StateItemsCount int             `json:"stateItemsCount"`
	AppAddress      string          `json:"appAddress"`
	Healthy         bool            `json:"healthy"`
}

type ActorMetadata struct {
	ActorType         string   `json:"actorType"`
	ActivatedContexts []string `json:"activatedContexts"`
}
