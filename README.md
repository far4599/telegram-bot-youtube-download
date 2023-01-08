# telegram-bot-youtube-download
Send a video url (YouTube, Vimeo, etc) to the bot, it will download the video and send it to your chat, if streaming service is supported.

## Preparation
Rename `.env.example` to `.env`. Update your bot token and app credentials in `.env`.
To get app credentials, visit page https://my.telegram.org/apps and create a new app, if you haven't created one yet.

### How to run with docker
```shell
docker run -d --memory="128m" --env-file .env far4599/youtube-download-telegram-bot:latest
```

### How to run with docker-compose
```
docker-compose up -d
```
