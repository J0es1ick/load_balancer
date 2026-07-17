FROM golang:1.24.3-alpine3.21 AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o balancer ./cmd/balancer

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/balancer .
COPY config /config

EXPOSE 8080

CMD ["./balancer"]
