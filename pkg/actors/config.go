// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import "time"

// Config is the actor runtime configuration
type Config struct {
	HostAddress                   string
	AppID                         string
	PlacementAddresses            []string
	HostedActorTypes              []string
	Port                          int
	HeartbeatInterval             time.Duration
	ActorDeactivationScanInterval time.Duration
	ActorIdleTimeout              time.Duration
	DrainOngoingCallTimeout       time.Duration
	DrainRebalancedActors         bool
	Namespace                     string
}

const (
	defaultActorIdleTimeout   = time.Minute * 60
	defaultHeartbeatInterval  = time.Second * 1
	defaultActorScanInterval  = time.Second * 30
	defaultOngoingCallTimeout = time.Second * 60
)

// NewConfig returns the actor runtime configuration
func NewConfig(hostAddress, appID string, placementAddresses []string, hostedActors []string, port int,
	actorScanInterval, actorIdleTimeout, ongoingCallTimeout string, drainRebalancedActors bool, namespace string) Config {
	c := Config{
		HostAddress:                   hostAddress,
		AppID:                         appID,
		PlacementAddresses:            placementAddresses,
		HostedActorTypes:              hostedActors,
		Port:                          port,
		HeartbeatInterval:             defaultHeartbeatInterval,
		ActorDeactivationScanInterval: defaultActorScanInterval,
		ActorIdleTimeout:              defaultActorIdleTimeout,
		DrainOngoingCallTimeout:       defaultOngoingCallTimeout,
		DrainRebalancedActors:         drainRebalancedActors,
		Namespace:                     namespace,
	}

	scanDuration, err := time.ParseDuration(actorScanInterval)
	if err == nil {
		c.ActorDeactivationScanInterval = scanDuration
	}

	idleDuration, err := time.ParseDuration(actorIdleTimeout)
	if err == nil {
		c.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(ongoingCallTimeout)
	if err == nil {
		c.DrainOngoingCallTimeout = drainCallDuration
	}

	return c
}
