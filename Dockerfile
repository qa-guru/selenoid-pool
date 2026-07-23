# syntax=docker/dockerfile:1

# --- build stage: static Go binary ---
FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/selenoid-warm-pool .

# --- runtime stage: minimal static image ---
FROM scratch
WORKDIR /app

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/selenoid-warm-pool /usr/local/bin/selenoid-warm-pool
COPY config.example.yaml /app/config.example.yaml

ENV WARM_POOL_CONFIG=/app/config.example.yaml \
    WARM_POOL_HOST=0.0.0.0 \
    WARM_POOL_PORT=9090

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/selenoid-warm-pool"]
