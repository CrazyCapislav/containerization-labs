# Сборка статического бинарника: тулчейн остаётся в первой стадии и в образ не едет.
FROM golang:1.26 AS build
WORKDIR /src

# Прокси нужен только на время сборки: провайдер режет часть реестров и
# модульный прокси Go, а туннель хоста до них достаёт.
ARG HTTP_PROXY
ARG HTTPS_PROXY

# go.mod и go.sum копируются отдельно от кода: слой с зависимостями
# пересобирается только при изменении самих зависимостей.
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /api .

# scratch подходит, потому что бинарник статический, а наружу сервис ходит
# только по обычному HTTP (OTLP до Jaeger и запросы к себе) — сертификаты не нужны.
FROM scratch
COPY --from=build /api /api
# 65534 — nobody. В scratch нет /etc/passwd, поэтому пользователь задаётся числом.
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/api"]
