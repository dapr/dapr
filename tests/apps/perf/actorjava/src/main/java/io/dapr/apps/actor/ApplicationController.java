package io.dapr.apps.actor;

import java.util.UUID;

import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.ResponseBody;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

import io.dapr.actors.ActorId;
import io.dapr.actors.client.ActorProxyBuilder;
import io.dapr.apps.actor.actors.DemoActorTimer;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import reactor.core.publisher.Mono;

@RestController
@Slf4j
@RequiredArgsConstructor
public class ApplicationController {

        private final ActorProxyBuilder<DemoActorTimer> demoActorProxyBuilder;

        @RequestMapping(value="/actors", method = RequestMethod.POST, consumes = "*")
        @ResponseStatus(HttpStatus.OK)
        @ResponseBody
        public Mono<Void> activateActor() {
                ActorId actorId = new ActorId(UUID.randomUUID().toString());
                return Mono.fromRunnable(() -> {
                        log.info("creating actor with id: {}", actorId);
                        DemoActorTimer demoActor = demoActorProxyBuilder.build(actorId);

                        demoActor.registerDemoActorTimer();
                }).doOnError(throwable -> log.warn("error in registerTimer() for actor ".concat(actorId.toString())
                                .concat(" - ").concat(throwable.getMessage()), throwable)).then();
        }

        @GetMapping(path = "/health")
        public Mono<Void> health() {
                return Mono.empty();
        }
}
