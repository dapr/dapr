package io.dapr.apps.actor;

import io.dapr.actors.client.ActorProxyBuilder;
import io.dapr.actors.client.ActorClient;
import io.dapr.apps.actor.actors.DemoActorTimer;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class ActorConfig {

    public static final ActorClient ACTOR_CLIENT = new ActorClient();

    @Bean
    public ActorProxyBuilder<DemoActorTimer> demoActorProxyBuilder() {
        return new ActorProxyBuilder<>(DemoActorTimer.class, ACTOR_CLIENT);
    }
}
