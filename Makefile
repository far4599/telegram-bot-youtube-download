build-and-push:
	@docker buildx build -t far4599/youtube-download-telegram-bot:latest --push --platform=linux/arm64,linux/amd64 .