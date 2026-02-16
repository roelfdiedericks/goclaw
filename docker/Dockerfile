# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o bin/goclaw ./cmd/goclaw

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -h /home/goclaw goclaw
USER goclaw
WORKDIR /home/goclaw

# Copy binary and entrypoint
COPY --from=builder /app/bin/goclaw /usr/local/bin/
COPY --chown=goclaw:goclaw docker/entrypoint.sh /entrypoint.sh

EXPOSE 1337

ENTRYPOINT ["/entrypoint.sh"]
