# telegram-bot-youtube-download
When you send youtube video url to the bot, it will provide links to download that video or audio

### How to run with docker
Replace "123456789:AAAeeeeeeeeeeeeeeeee" with your Telegram bot api token
```shell
docker build -t youtube-bot .
docker run -d --memory="128m" --cpus="2" -e BOT_TOKEN=123456789:AAAeeeeeeeeeeeeeeeee youtube-bot
```

### How to run with docker-compose 
Replace "123456789:AAAeeeeeeeeeeeeeeeee" with your Telegram bot api token in docker-compose.yml
```
docker-compose up --build -d
```
