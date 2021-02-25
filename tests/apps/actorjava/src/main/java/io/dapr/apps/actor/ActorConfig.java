/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor;

import java.util.HashMap;
import java.util.Map;
import java.util.function.Function;

import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import io.dapr.actors.client.ActorProxy;
import io.dapr.actors.client.ActorProxyBuilder;

@Configuration
public class ActorConfig {

    private static final Map<String, ActorProxyBuilder<ActorProxy>> BUILDERS = new HashMap<>();

    @Bean(name = "actorProxyBuilderFunction")
    public Function<String, ActorProxyBuilder<ActorProxy>> actorProxyBuilderFunction() {
        return actorType -> {
            synchronized(BUILDERS) {
                ActorProxyBuilder<ActorProxy> builder = BUILDERS.get(actorType);
                if (builder != null) {
                    return builder;
                }

                builder = new ActorProxyBuilder<>(actorType, ActorProxy.class);
                BUILDERS.put(actorType, builder);
                return builder;
            }
        };
    }
}
