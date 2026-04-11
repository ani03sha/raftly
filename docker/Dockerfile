# Stage 1: build
FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/raftly-server ./cmd/server


# Stage 2: minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/raftly-server /bin/raftly-server

EXPOSE 7001 8001

ENTRYPOINT ["/bin/raftly-server"]