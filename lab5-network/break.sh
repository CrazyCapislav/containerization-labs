#!/usr/bin/env bash
# Вносит ОДНУ случайную поломку связности в политики чарта (Часть 6 лабы 5).
#
# Все варианты — правдоподобные ошибки конфигурации, а не выдуманные: такие
# опечатки реально делают в политиках. Каждый вносится через значения чарта,
# шаблоны при этом не трогаются: то, что ломается значением, значением же
# и чинится.
#
#   ./break.sh            внести случайную поломку
#   ./break.sh --restore  вернуть исправное состояние
#   ./break.sh --reveal   показать, что было внесено (после разбора!)
#
# Выбор пишется в docs/.break-answer в base64 — не для секретности, а чтобы
# не прочитать случайно, пролистывая каталог.
set -uo pipefail

R="$(cd "$(dirname "$0")/.." && pwd)"
CHART="$R/lab3-platform/helm/shop"
ANSWER="$R/lab5-network/docs/.break-answer"
NS=shop

restore() {
  # --reset-values обязателен: helm upgrade без -f и без --set переиспользует
  # значения прошлого релиза, то есть вернул бы ту же поломку.
  helm upgrade shop "$CHART" -n "$NS" --reset-values --wait --timeout 3m >/dev/null 2>&1 &&
    echo "Исправное состояние восстановлено." ||
    echo "Не удалось восстановить, смотри helm status shop -n $NS" >&2
  # Разгадку не удаляем, а переименовываем: после починки её ещё надо
  # сверить с диагнозом, и стирать её здесь — значит лишить себя проверки.
  [ -f "$ANSWER" ] && mv "$ANSWER" "${ANSWER}.solved"
  return 0
}

reveal() {
  if [ -f "$ANSWER" ]; then base64 -d < "$ANSWER"; echo
  elif [ -f "${ANSWER}.solved" ]; then
    echo "Последняя поломка (уже устранена):"; base64 -d < "${ANSWER}.solved"; echo
  else echo "Поломка не внесена."; fi
}

case "${1:-}" in
  --restore) restore; exit $? ;;
  --reveal)  reveal;  exit 0  ;;
esac

# Варианты. Каждый — пара: описание и набор переопределений values.
V=$((RANDOM % 4 + 1))

case $V in
  1)
    DESC="Вариант 1. Неверная метка в allow-правиле: источник потока к базе
указан как api-v2 вместо api. Политика существует, селектор валиден,
но не выбирает ни одного пода. Симптом: заказы перестают проходить."
    ARGS=(--set-json 'networkPolicy.flows=[{"from":"client","to":"api","port":8080},{"from":"api-v2","to":"postgres","port":5432},{"from":"worker","to":"postgres","port":5432}]')
    ;;
  2)
    DESC="Вариант 2. Порт в правиле не совпадает с портом сервиса: 5433
вместо 5432. Правило применяется, стороны выбраны верно, а пакет
не подходит под toPorts. Симптом: заказы перестают проходить."
    ARGS=(--set-json 'networkPolicy.flows=[{"from":"client","to":"api","port":8080},{"from":"api","to":"postgres","port":5433},{"from":"worker","to":"postgres","port":5432}]')
    ;;
  3)
    DESC="Вариант 3. Из списка клиентов DNS убраны api и worker. Соединение
к базе разрешено, но имя shop-db-rw перестаёт разрешаться.
Симптом: заказы не проходят, в логах ошибка резолва, а не отказ соединения."
    ARGS=(--set-json 'networkPolicy.dns.clients=["client","intruder"]')
    ;;
  4)
    DESC="Вариант 4. Подменена метка подов Prometheus в правиле сбора метрик.
Приложение работает полностью, ломается только наблюдаемость:
цели уходят в down, метрики shop перестают поступать."
    ARGS=(--set-json 'networkPolicy.monitoring.podLabels={"app.kubernetes.io/name":"prometheus-server"}')
    ;;
esac

if helm upgrade shop "$CHART" -n "$NS" "${ARGS[@]}" --wait --timeout 3m >/dev/null 2>&1; then
  printf '%s\n' "$DESC" | base64 -w0 > "$ANSWER"
  echo "Поломка внесена. Что именно — записано в docs/.break-answer."
  echo "Диагностируй по симптому и карте трафика, потом ./break.sh --reveal."
else
  echo "helm upgrade не прошёл, поломка не внесена" >&2
  exit 1
fi
