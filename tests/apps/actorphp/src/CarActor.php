<?php

namespace Test;

use Dapr\Actors\Actor;
use Dapr\Actors\Attributes\DaprType;
use Dapr\Deserialization\Deserializer;
use Dapr\Serialization\Serializer;

#[DaprType('PHPCarActor')]
class CarActor extends Actor implements ICarActor
{

    private int $count = 0;

    public function __construct(public string $id)
    {
        parent::__construct($id);
    }

    public function IncrementAndGetAsync(int|null $delta): int
    {
        $this->count += $delta;

        return $this->count;
    }

    public function CarFromJSONAsync(string $content): Car
    {
        return Deserializer::from_json(Car::class, $content);
    }

    public function CarToJSONAsync(Car $car): string
    {
        return Serializer::as_json($car);
    }
}
