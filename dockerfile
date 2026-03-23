# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY src/ ./src/
RUN go build -o server ./src/entrypoints/

# Run stage
FROM alpine:3.21

RUN apk add --no-cache curl

WORKDIR /app

COPY --from=builder /app/server .
COPY --from=builder /app/src/migrations ./src/migrations

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

CMD ["./server"]
