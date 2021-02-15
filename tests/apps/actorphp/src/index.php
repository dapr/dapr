<?php

use Dapr\Actors\ActorProxy;
use Dapr\App;
use Dapr\Attributes\FromBody;
use DI\ContainerBuilder;
use Test\Car;
use Test\CarActor;
use Test\ICarActor;

require_once __DIR__.'/../vendor/autoload.php';

$app = App::create(
    configure: fn(ContainerBuilder $builder) => $builder->addDefinitions(['dapr.log.level' => \Psr\Log\LogLevel::DEBUG, 'dapr.actors' => fn() => [CarActor::class]])
);

$app->post(
    '/incrementAndGet/{actor_name}/{id}',
    fn(string $actor_name, string $id, ActorProxy $actorProxy) => $actorProxy->get(
        ICarActor::class,
        $id,
        $actor_name
    )->IncrementAndGetAsync(1)
);
$app->post(
    '/carFromJSON/{actor_name}/{id}',
    fn(string $actor_name, string $id, ActorProxy $actorProxy, #[FromBody] array $json) => $actorProxy->get(
        ICarActor::class,
        $id,
        $actor_name
    )->CarFromJSONAsync(json_encode($json))
);
$app->post(
    '/carToJSON/{actor_name}/{id}',
    fn(string $actor_name, string $id, #[FromBody] Car $car, ActorProxy $actorProxy) => json_decode(
        $actorProxy->get(
            ICarActor::class,
            $id,
            $actor_name
        )->CarToJSONAsync($car)
    )
);

$app->start();
