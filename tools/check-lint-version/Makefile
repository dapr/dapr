.PHONY: cover
cover:
	go test -coverprofile=cover.out ./ && go tool cover -html=cover.out

.PHONY: tidy
tidy: ## Updates the go modules
	go mod tidy

.PHONY: test
test:
	go test -count=1 \
			-race \
			-coverprofile=coverage.txt \
			-covermode=atomic \
			./...

.PHONY: lint
lint: check-lint-version
	golangci-lint run --timeout=3m --config ../../.golangci.yml

check-lint-version: tidy
	go run main.go