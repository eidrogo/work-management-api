# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN set -e; CGO_ENABLED=0 go test ./... && mkdir -p /out && \
    for command in api worker migrate seed replay-notification; do \
      CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "/out/${command}" "./cmd/${command}"; \
    done

FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241 AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/* && \
    groupadd --system app && useradd --system --gid app --home-dir /app app
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations/ /app/migrations/
USER app
EXPOSE 8080 9091
ENTRYPOINT ["/app/api"]
