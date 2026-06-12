# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY src/ ./src/
# Static binary (CGO off) with symbol table and DWARF stripped for a smaller image.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server ./src/entrypoints/

# Run stage
FROM alpine:3.21

RUN apk add --no-cache curl \
    && addgroup -S app && adduser -S -G app app

WORKDIR /app

COPY --from=builder /app/server .

# Drop privileges: run as a non-root user.
USER app

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

CMD ["./server"]
