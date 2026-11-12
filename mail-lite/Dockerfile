FROM node:22-alpine AS ui
WORKDIR /src/web/ui
COPY web/ui/package*.json ./
RUN npm ci --ignore-scripts --no-audit --no-fund
COPY web/ui/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mail-lite ./cmd/mail-lite

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -g 10001 mail && adduser -D -u 10001 -G mail mail && mkdir /data && chown mail:mail /data
COPY --from=build /mail-lite /usr/local/bin/mail-lite
USER 10001:10001
ENV DATA_DIR=/data LISTEN_ADDR=:8080
EXPOSE 8080 2525
VOLUME ["/data"]
ENTRYPOINT ["mail-lite"]
