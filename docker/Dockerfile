# current directory must be ./dist

FROM gcr.io/distroless/static:nonroot
ARG PKG_FILES
WORKDIR /
COPY /$PKG_FILES /
USER 65532:65532
