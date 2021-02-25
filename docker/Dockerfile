FROM alpine:latest as alpine
RUN apk add -U --no-cache ca-certificates
# current directory must be ./dist

FROM gcr.io/distroless/static:nonroot
ARG PKG_FILES
WORKDIR /
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY /$PKG_FILES /
