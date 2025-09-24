FROM golang:1.24.6-bullseye as build_env

ENV CGO_ENABLED=0
WORKDIR /app
COPY *.go go.mod go.sum ./
RUN go get -d -v && go build -o tester .

FROM golang:1.22-bullseye as fortio_build_env

WORKDIR /fortio
ADD "https://api.github.com/repos/dapr/fortio/branches/v1.38.4-dapr" skipcache
RUN git clone https://github.com/dapr/fortio.git
RUN cd fortio && git checkout v1.38.4-dapr && go build

FROM debian:bullseye-slim
WORKDIR /
COPY --from=build_env /app/tester /
COPY --from=fortio_build_env /fortio/fortio/fortio /usr/local/bin
CMD ["/tester"]
