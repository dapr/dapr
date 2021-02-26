/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor;

import java.util.function.Function;

import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.ResponseBody;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

import io.dapr.actors.ActorId;
import io.dapr.actors.client.ActorProxy;
import io.dapr.actors.client.ActorProxyBuilder;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import reactor.core.publisher.Mono;

@RestController
@Slf4j
@RequiredArgsConstructor
public class ApplicationController {

        @Autowired
        @Qualifier("actorProxyBuilderFunction")
        private final Function<String, ActorProxyBuilder<ActorProxy>> actorProxyBuilderGetter;

        @RequestMapping(
                value="/incrementAndGet/{actorType}/{actorId}",
                method = RequestMethod.POST,
                consumes = "*")
        @ResponseStatus(HttpStatus.OK)
        @ResponseBody
        public Mono<Integer> incrementAndGet(
                @PathVariable String actorType,
                @PathVariable String actorId) {
                log.info(String.format("/incrementAndGet/%s/%s", actorType, actorId));
                ActorId id = new ActorId(actorId);
                ActorProxy proxy = this.actorProxyBuilderGetter.apply(actorType).build(id);
                return proxy.invokeActorMethod("IncrementAndGetAsync", 1, int.class);
        }

        @RequestMapping(
                value="/carFromJSON/{actorType}/{actorId}",
                method = RequestMethod.POST,
                consumes = "*")
        @ResponseStatus(HttpStatus.OK)
        @ResponseBody
        public Mono<Car> carFromJSON(
                @PathVariable String actorType,
                @PathVariable String actorId,
                @RequestBody String json) {
                log.info(String.format("/carFromJSON/%s/%s", actorType, actorId));
                ActorId id = new ActorId(actorId);
                ActorProxy proxy = this.actorProxyBuilderGetter.apply(actorType).build(id);
                return proxy.invokeActorMethod("CarFromJSONAsync", json, Car.class);
        }

        @RequestMapping(
                value="/carToJSON/{actorType}/{actorId}",
                method = RequestMethod.POST,
                consumes = "*")
        @ResponseStatus(HttpStatus.OK)
        @ResponseBody
        public Mono<String> carToJSON(
                @PathVariable String actorType,
                @PathVariable String actorId,
                @RequestBody Car car) {
                log.info(String.format("/carToJSON/%s/%s", actorType, actorId));
                ActorId id = new ActorId(actorId);
                ActorProxy proxy = this.actorProxyBuilderGetter.apply(actorType).build(id);
                return proxy.invokeActorMethod("CarToJSONAsync", car, String.class);
        }
}
