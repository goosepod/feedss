FROM golang:1.27.0-alpine3.24 AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/feedss .

FROM alpine:3.24.1
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/feedss /app/feedss
ENV APP_ENV=production
ENV APP_PORT=4317
EXPOSE 4317
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -q --spider "http://127.0.0.1:${APP_PORT}/login" || exit 1
CMD ["/app/feedss"]
