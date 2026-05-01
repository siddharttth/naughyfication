FROM golang:1.24.1-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./

# Retry go mod download to handle transient network errors
RUN go mod download || go mod download || go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/naughtyfication ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=builder /out/naughtyfication /app/naughtyfication
EXPOSE 8080
CMD ["/app/naughtyfication"]
