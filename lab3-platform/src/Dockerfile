# Один Dockerfile на оба сервиса: отличается только собираемая команда.
# Так слой с зависимостями переиспользуется обеими сборками.
FROM golang:1.26 AS build
WORKDIR /src

ARG HTTP_PROXY
ARG HTTPS_PROXY

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Какой бинарник собирать, решает аргумент: api или worker.
ARG CMD=api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/${CMD}

FROM scratch
COPY --from=build /app /app
# 65534 это nobody. Политики из Части 1 потребуют запуска не от root,
# поэтому пользователь задан сразу, а не добавлен потом под давлением.
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/app"]
