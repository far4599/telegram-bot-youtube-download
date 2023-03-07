FROM golang:1.20-alpine as builder
WORKDIR /src
COPY . .
RUN go mod download && CGO_ENABLED=0 GOOS=linux go build -a -o app ./cmd/app/app.go

FROM alpine
RUN apk add --no-cache --update ca-certificates yt-dlp
WORKDIR /app
COPY --from=builder /src/app .
ENTRYPOINT ["./app"]