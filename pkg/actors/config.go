package actors

import "time"

type Config struct {
	HostAddress             string
	ActionsID               string
	PlacementServiceAddress string
	HostedActorTypes        []string
	Port                    int
	HeartbeatInterval       time.Duration
}
