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
