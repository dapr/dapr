#!/bin/bash

set -xe

## go-fuzz doesn't support modules for now, so ensure we do everything
## in the old style GOPATH way
export GO111MODULE="off"

# We need to download these dependencies again after we set GO111MODULE="off"
go get -t -v ./...

go get github.com/dvyukov/go-fuzz/go-fuzz github.com/dvyukov/go-fuzz/go-fuzz-build

wget -q -O fuzzitbin https://github.com/fuzzitdev/fuzzit/releases/download/v2.4.52/fuzzit_Linux_x86_64
chmod a+x fuzzitbin

for w in request response cookie url; do
	go-fuzz-build -libfuzzer -o fasthttp_$w.a ./fuzzit/$w/
	clang -fsanitize=fuzzer fasthttp_$w.a -o fasthttp_$w

	./fuzzitbin create job --type $1 fasthttp/$w fasthttp_$w
done
