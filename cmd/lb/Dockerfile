FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o lb ./cmd/lb

FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/lb .
EXPOSE 8080
ENTRYPOINT ["./lb"]