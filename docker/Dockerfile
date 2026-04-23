# Stage 1: build the Vite UI
FROM node:20-alpine AS ui-builder
WORKDIR /ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY ui/ ./
RUN npm run build


# Stage 2: build Go binary with the built UI embedded
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Replace the placeholder web/dist with the freshly built UI
RUN rm -rf web/dist
COPY --from=ui-builder /ui/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/raftly-server ./cmd/server


# Stage 3: minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/raftly-server /bin/raftly-server

EXPOSE 7001 8001

ENTRYPOINT ["/bin/raftly-server"]
