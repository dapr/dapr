package io.dapr.apps.actor;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;

import io.dapr.actors.runtime.ActorRuntime;
import io.dapr.apps.actor.actors.DemoActorTimerImpl;

@SpringBootApplication(scanBasePackages = { "io.dapr.apps.actor", "io.dapr.springboot"})
public class Application {

    public static void main(String[] args) {
        ActorRuntime.getInstance().registerActor(DemoActorTimerImpl.class);
        SpringApplication.run(Application.class, args);
    }

}
