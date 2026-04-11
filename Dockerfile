FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.work go.work.sum ./
COPY apps/server/ apps/server/
COPY apps/cli/ apps/cli/
COPY apps/mcp/ apps/mcp/
RUN apk add --no-cache nodejs npm
WORKDIR /src/apps/server/gallery
RUN npm ci && npm run build
WORKDIR /src/apps/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags '-X main.version=k8s-dev' -o /berth-server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates sqlite
COPY --from=builder /berth-server /usr/local/bin/berth-server
EXPOSE 3456
ENTRYPOINT ["berth-server"]
