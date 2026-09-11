# Build Stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build lightweight signaling server binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /signaling ./cmd/signaling

# Final Minimal Stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /root/
COPY --from=builder /signaling /signaling

EXPOSE 8080

CMD ["/signaling"]
