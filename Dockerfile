# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/server .

# Final stage: minimal and secure Google Distroless non-root image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

# Copy compiled binary from builder
COPY --from=builder /app/server /server

# Cloud Run expects the container to listen on $PORT (defaults to 8080)
ENV PORT=8080
EXPOSE 8080

# Run as non-root user
USER nonroot:nonroot

ENTRYPOINT ["/server"]
