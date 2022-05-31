<?php

namespace Test;

use Dapr\Actors\Attributes\DaprType;

#[DaprType('PHPCarActor')]
interface ICarActor
{
    public function IncrementAndGetAsync(int $delta): int;

    public function CarToJSONAsync(Car $car): string;

    public function CarFromJSONAsync(string $content): Car;
}
