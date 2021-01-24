<?php

use Dapr\Actors\ActorProxy;
use Dapr\Actors\ActorRuntime;
use Dapr\Actors\ProxyModes;
use Dapr\Attributes\FromBody;
use Dapr\Attributes\FromRoute;
use Dapr\Runtime;
use Monolog\Handler\ErrorLogHandler;
use Monolog\Logger;
use Monolog\Processor\HostnameProcessor;
use Monolog\Processor\PsrLogMessageProcessor;
use Psr\Log\LogLevel;
use Test\Car;
use Test\CarActor;
use Test\ICarActor;

error_reporting(E_ALL);
ini_set("display_errors", 0);
set_error_handler(
    function ($err_no, $err_str, $err_file, $err_line, $err_context = null) {
        http_response_code(500);
        echo json_encode(
            [
                'errorCode' => 'Exception',
                'message' => (E_WARNING & $err_no ? 'WARNING' : (E_NOTICE & $err_no ? 'NOTICE' : (E_ERROR & $err_no ? 'ERROR' : 'OTHER'))) . ': ' . $err_str,
                'file'    => $err_file,
                'line'    => $err_line,
            ]
        );
        die();
    },
    E_ALL
);

require_once __DIR__.'/../vendor/autoload.php';

$logger = new Logger('DAPR');
$logger->pushProcessor(new PsrLogMessageProcessor());
$logger->pushProcessor(new HostnameProcessor());
$logger->pushHandler(new ErrorLogHandler(level: LogLevel::DEBUG));
Runtime::set_logger($logger);

class Methods {
    public static function car_from_json(#[FromRoute] string $actor_name, #[FromRoute] string $id, #[FromBody] array $json) {
        global $logger;
        $logger->debug('Got json: {json}', ['json' => $json]);
        return ActorProxy::get(ICarActor::class, $id, $actor_name)->CarFromJSONAsync(json_encode($json));
    }

    public function car_to_json(#[FromRoute] string $actor_name, #[FromRoute] string $id, #[FromBody] Car $car) {
        return json_decode(ActorProxy::get(ICarActor::class, $id, $actor_name)->CarToJSONAsync($car));
    }
}
$methods = new Methods;

ActorRuntime::register_actor(CarActor::class);
Runtime::register_method(http_method: 'POST', method_name: 'incrementAndGet', callback: fn(#[FromRoute] $actor_name, #[FromRoute] $id) => ActorProxy::get(ICarActor::class, $id, $actor_name)->IncrementAndGetAsync(1));
Runtime::register_method(http_method: 'POST', method_name: 'carFromJSON', callback: [Methods::class, 'car_from_json']);
Runtime::register_method(http_method: 'POST', method_name: 'carToJSON', callback: [$methods, 'car_to_json']);

header('Content-Type: application/json');
$result = Runtime::get_handler_for_route($_SERVER['REQUEST_METHOD'], strtok($_SERVER['REQUEST_URI'], '?'))();
http_response_code($result['code']);
if (isset($result['body'])) {
    echo $result['body'];
}
