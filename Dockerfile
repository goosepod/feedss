FROM golang:1.22.5-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/feedss .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/feedss /app/feedss
COPY templates ./templates
COPY static ./static
ENV APP_ENV=production
EXPOSE 8080
CMD ["/app/feedss"]
