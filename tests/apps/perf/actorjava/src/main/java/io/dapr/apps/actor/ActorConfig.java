package io.dapr.apps.actor;

import io.dapr.actors.client.ActorProxyBuilder;
import io.dapr.apps.actor.actors.DemoActorTimer;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class ActorConfig {

    @Bean
    public ActorProxyBuilder<DemoActorTimer> demoActorProxyBuilder() {
        return new ActorProxyBuilder<>(DemoActorTimer.class);
    }
}
