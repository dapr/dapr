## Prequisites

1. Go: [https://golang.org/dl/](https://golang.org/dl/)
1. Golint `go get -u -v github.com/golang/lint/golint`

## Contributing

The workflow is pretty standard:

1. Fork github.com/streadway/amqp
1. Add the pre-commit hook: `ln -s ../../pre-commit .git/hooks/pre-commit`
1. Create your feature branch (`git checkout -b my-new-feature`)
1. Run integration tests (see below)
1. **Implement tests**
1. Implement fixs
1. Commit your changes (`git commit -am 'Add some feature'`)
1. Push to a branch (`git push -u origin my-new-feature`)
1. Submit a pull request

## Running Tests

The test suite assumes that:

 * A RabbitMQ node is running on localhost with all defaults: [https://www.rabbitmq.com/download.html](https://www.rabbitmq.com/download.html)
 * `AMQP_URL` is exported to `amqp://guest:guest@127.0.0.1:5672/`

### Integration Tests

After starting a local RabbitMQ, run integration tests with the following:

    env AMQP_URL=amqp://guest:guest@127.0.0.1:5672/ go test -v -cpu 2 -tags integration -race

All integration tests should use the `integrationConnection(...)` test
helpers defined in `integration_test.go` to setup the integration environment
and logging.
