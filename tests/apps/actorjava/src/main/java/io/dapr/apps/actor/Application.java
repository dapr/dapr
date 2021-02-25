/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor;

import java.time.Duration;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

import io.dapr.actors.runtime.ActorRuntime;
import io.dapr.apps.actor.actors.CarActorImpl;

@SpringBootApplication(scanBasePackages = { "io.dapr.apps.actor", "io.dapr.springboot"})
public class Application {

    public static void main(String[] args) {
        ActorRuntime.getInstance().getConfig().setActorIdleTimeout(Duration.ofSeconds(1));
        ActorRuntime.getInstance().getConfig().setActorScanInterval(Duration.ofSeconds(1));
        ActorRuntime.getInstance().registerActor(CarActorImpl.class);
        SpringApplication.run(Application.class, args);
    }

}
