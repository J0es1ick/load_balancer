FROM golang:1.25.12-alpine3.23 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/balancer ./cmd/balancer

FROM alpine:3.23.5

WORKDIR /app
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder --chown=10001:10001 /out/balancer /app/balancer

USER 10001:10001
EXPOSE 8080 9090
ENTRYPOINT ["/app/balancer"]
