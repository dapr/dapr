export GO111MODULE=on

default: fmt vet errcheck test lint

# Taken from https://github.com/codecov/example-go#caveat-multiple-files
.PHONY: test
test:
	echo "" > coverage.txt
	for d in `go list ./...`; do \
		go test -p 1 -v -timeout 240s -race -coverprofile=profile.out -covermode=atomic $$d || exit 1; \
		if [ -f profile.out ]; then \
			cat profile.out >> coverage.txt; \
			rm profile.out; \
		fi \
	done

GOLINT := $(shell command -v golint)

.PHONY: lint
lint:
ifndef GOLINT
	go get golang.org/x/lint/golint
endif
	go list ./... | xargs golint

.PHONY: vet
vet:
	go vet ./...

ERRCHECK := $(shell command -v errcheck)
# See https://github.com/kisielk/errcheck/pull/141 for details on ignorepkg
.PHONY: errcheck
errcheck:
ifndef ERRCHECK
	go get github.com/kisielk/errcheck
endif
	errcheck -ignorepkg fmt github.com/Shopify/sarama/...

.PHONY: fmt
fmt:
	@if [ -n "$$(go fmt ./...)" ]; then echo 'Please run go fmt on your code.' && exit 1; fi

.PHONY : install_dependencies
install_dependencies: get

.PHONY: get
get:
	go get -t -v ./...

.PHONY: clean
clean:
	go clean ./...
