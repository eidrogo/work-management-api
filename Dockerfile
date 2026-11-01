# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS toolchain

FROM toolchain AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN set -e; CGO_ENABLED=0 go test ./... && mkdir -p /out && \
    for command in api worker migrate seed replay-notification healthcheck; do \
      CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "/out/${command}" "./cmd/${command}"; \
    done

FROM scratch AS runtime
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
WORKDIR /app
COPY --from=toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/ /app/
COPY --chown=65532:65532 migrations/ /app/migrations/
USER 65532:65532
EXPOSE 8080 9090 9091
ENTRYPOINT ["/app/api"]
