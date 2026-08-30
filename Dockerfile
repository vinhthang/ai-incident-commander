# Build stage
FROM --platform=$BUILDPLATFORM golang:alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Compile static binary for target arch
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -a -installsuffix cgo -o incident-commander main.go

# Run stage
FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update && apt-get install -y ca-certificates git openssh-client curl && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/incident-commander .
EXPOSE 8085
CMD ["./incident-commander"]
