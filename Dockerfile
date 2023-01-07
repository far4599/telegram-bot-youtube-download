FROM golang:1.19-alpine as builder
WORKDIR /src
COPY . .
RUN go mod download && CGO_ENABLED=0 GOOS=linux go build -a -o app ./cmd/app/app.go

FROM alpine
RUN apk add --update ca-certificates yt-dlp && rm -rf /tmp/* /var/cache/apk/*
COPY --from=builder /src/app /app
ENTRYPOINT ["/app"]