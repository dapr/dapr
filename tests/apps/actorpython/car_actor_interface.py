# -*- coding: utf-8 -*-
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.

from dapr.actor import ActorInterface, actormethod


class CarActorInterface(ActorInterface):
    @actormethod(name="IncrementAndGetAsync")
    async def increment_and_get(self, delta: int) -> int:
        ...

    @actormethod(name="CarFromJSONAsync")
    async def car_from_json(self, json_content: str) -> object:
        ...

    @actormethod(name="CarToJSONAsync")
    async def car_to_json(self, car: object) -> str:
        ...
