# syntax=docker/dockerfile:1.7
FROM golang:1.23.2-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libpq-dev pkg-config ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=1 go test ./... && mkdir -p /out && \
    for command in api worker migrate seed replay-notification; do \
      CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "/out/${command}" "./cmd/${command}"; \
    done

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends libpq5 ca-certificates curl && rm -rf /var/lib/apt/lists/* && \
    groupadd --system app && useradd --system --gid app --home-dir /app app
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations/ /app/migrations/
USER app
EXPOSE 8080 9091
ENTRYPOINT ["/app/api"]
