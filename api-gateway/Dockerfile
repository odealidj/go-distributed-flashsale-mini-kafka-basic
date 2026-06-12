# Stage 1: Build binary using standard Golang environment
FROM docker.io/library/golang:1.26-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app

# --- Layer Cache Optimization ---
# Copy Go workspace manifests first so Docker can cache the module download
# layer independently from source code changes.
COPY go.work go.work.sum ./

# Copy all go.mod and go.sum from every module in the workspace
COPY api-gateway/go.mod api-gateway/go.sum ./api-gateway/
COPY auth-service/go.mod auth-service/go.sum ./auth-service/
COPY inventory-service/go.mod inventory-service/go.sum ./inventory-service/
COPY order-service/go.mod order-service/go.sum ./order-service/
COPY payment-service/go.mod payment-service/go.sum ./payment-service/
COPY product-service/go.mod product-service/go.sum ./product-service/
COPY proto/go.mod proto/go.sum ./proto/
COPY shared/go.mod shared/go.sum ./shared/

# Download all dependencies. This layer is cached as long as go.mod/go.sum
# files are unchanged, even if the actual source code is modified.
RUN go mod download

# --- Source Code ---
# Copy the full workspace source code only after dependencies are cached
COPY . .

# Compile the binary with optimizations (strip debug symbols for smaller size)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/api-gateway ./api-gateway/cmd/api-gateway

# Stage 2: Final production image
FROM docker.io/library/alpine:3.18
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

# Copy compiled binary from builder stage
COPY --from=builder /app/bin/api-gateway .

# Expose internal service port
EXPOSE 8000

# Run service
CMD ["./api-gateway"]
