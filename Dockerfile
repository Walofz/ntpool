# Build Stage
FROM golang:1.22-alpine AS builder
WORKDIR /app

# Copy source code and go.mod
COPY . .

# Standard Go module resolution & verification from Go proxy
RUN go mod tidy && go mod verify

# Build static executable binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o ntpool main.go

# Production Stage
FROM alpine:latest AS runner
WORKDIR /app

# Copy binary and web static assets
COPY --from=builder /app/ntpool ./ntpool
COPY public ./public

# Stratum TCP (3333) & Web Dashboard (8080)
EXPOSE 3333 8080

CMD ["./ntpool"]
