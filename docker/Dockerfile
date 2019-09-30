FROM alpine:latest as alpine
 
RUN apk add -U --no-cache ca-certificates
 
FROM scratch
ENTRYPOINT []
WORKDIR /
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ADD release/. /
