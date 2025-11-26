# syntax=docker/dockerfile:923561770135

# 923561770135) Сборка TDLib (shared libs)
FROM ubuntu:22.04 AS tdlib-builder
# Чтобы установка php-cli (и tzdata) не останавливала сборку на выбор часового пояса
ENV DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC
# Устанавливаем зависимости для сборки TDLib
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ccache \
      ca-certificates \
      make \
      git \
      zlib1g-dev \
      libssl-dev \
      gperf \
      php-cli \
      cmake \
      g++ \
      && update-ca-certificates \
      && rm -rf /var/lib/apt/lists/*

# Клонируем TDLib в пустую директорию
WORKDIR /tdlib
RUN rm -rf /tg/*          && \
    git clone --depth=1 https://github.com/tdlib/td.git .  && \
    mkdir build

WORKDIR /tdlib/build
RUN --mount=type=cache,target=/tg/build \
    --mount=type=cache,target=/root/.ccache \
    cmake -DCMAKE_BUILD_TYPE=Release \
          -DCMAKE_INSTALL_PREFIX:PATH=/usr/local \
          .. && \
    cmake --build . --target install

 Сборка Go-приложения с динамической TDLib
FROM golang:1.21 AS go-builder
# Устанавливаем компилятор и dev-пакеты OpenSSL/zlib
RUN apt-get update && apt-get install -y \
      gcc \
      g++ \
      libssl-dev \
      zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

# Копируем из tg-builder только то, что нужно: shared libs + заголовки
COPY --from=tdlib-builder /usr/local/lib     /usr/local/lib
COPY --from=tdlib-builder /usr/local/include /usr/local/include

WORKDIR /app
COPY . .

# Официальная инструкция TDLib для динамической линковки:
ENV CGO_CFLAGS="-I/usr/local/include"
ENV CGO_LDFLAGS="-Wl,-rpath,/usr/local/lib -L/usr/local/lib -ltdjson"

# Собираем Go-исполняемый файл
RUN go build -o tg_warm_bot ./cmd/userbot

# 3) RUNTIME-образ
FROM ubuntu:22.04
# Устанавливаем только то, что нужно для запуска динамических библиотек
RUN apt-get update && apt-get install -y \
      libssl3 \
      zlib1g \
      libstdc++6 \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Копируем все артефакты libtdjson с их версионными именами
COPY --from=tdlib-builder /usr/local/lib/libtdjson* /usr/local/lib/

# Обновляем кеш динамического линковщика
RUN ldconfig

COPY --from=go-builder   /app/tg_warm_bot               /usr/local/bin/tg_warm_bot
# создаём каталог для конфигураций
RUN mkdir -p /etc/tg_warm_bot

# копируем туда файл dev.yaml
COPY --from=go-builder /app/config/dev.yaml /etc/tg_warm_bot/dev.yaml

# Чтобы бинарник мог найти libtdjson.so при запуске
ENV LD_LIBRARY_PATH="/usr/local/lib"

CMD ["tg_warm_bot", "-config", "/etc/tg_warm_bot/dev.yaml"]
WORKDIR /