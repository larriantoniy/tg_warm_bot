# Указываем имя бинарного файла и Docker-образа
BINARY_NAME := tg-user-bot
IMAGE_NAME := telegram-user-bot-app

# Сборка Go-приложения
build:
	go build -o bin/$(BINARY_NAME) .

# Сборка Docker-образа
docker-build:
	docker build -t $(IMAGE_NAME):latest .

# Локальный запуск с Docker Compose (сбилдить и запустить)
docker-up:
	docker compose up -d --build

# Остановка всех контейнеров
docker-down:
	docker compose down

# Инициализация RediSearch
init-redis:
	docker compose exec redis bash -c "bash /scripts/init_redis_search.sh"