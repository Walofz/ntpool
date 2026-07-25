# Build Stage
FROM golang:1.22-alpine AS builder
WORKDIR /app

# Download dependencies
COPY go.mod go.sum* ./
RUN go mod download || true

# Copy source code
COPY . .

# Build standalone binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o ntpool main.go

# Production Stage
FROM alpine:latest AS runner
WORKDIR /app

# Copy binary and public static assets
COPY --from=builder /app/ntpool ./ntpool
COPY public ./public

# Stratum TCP Port (3333) & Web Dashboard HTTP Port (8080)
EXPOSE 3333 8080

# Run ntpool
CMD ["./ntpool"]
