FROM golang:1.19 as build_env

ENV CGO_ENABLED=0
WORKDIR /app
COPY app.go go.mod ./
RUN go get -d -v && go build -o tester .

FROM golang:1.19 as fortio_build_env

WORKDIR /fortio
ADD "https://api.github.com/repos/shubham1172/dapr-fortio/branches/shubham1172/add-subscribe-support" skipcache
RUN git clone https://github.com/shubham1172/dapr-fortio.git
RUN cd dapr-fortio && git checkout shubham1172/add-subscribe-support && go build

FROM debian:buster-slim
WORKDIR /
COPY --from=build_env /app/tester /
COPY --from=fortio_build_env /fortio/dapr-fortio/fortio /usr/local/bin
CMD ["/tester"]
