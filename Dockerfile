FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod ./
COPY go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X github.com/Daviey/mockllm/internal/server.Version=$(git describe --tags --always 2>/dev/null || echo dev)" -o mockllm ./cmd/mockllm

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/mockllm /usr/local/bin/mockllm
COPY --from=builder /build/providers /providers
EXPOSE 8080
ENTRYPOINT ["mockllm"]
CMD ["-specs", "/providers"]
