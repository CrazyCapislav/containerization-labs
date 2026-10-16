#!/usr/bin/env bash
# chaos.sh — случайно удаляет поды api/worker в namespace shop с заданным интервалом.
# Каждое убийство печатается с временной меткой и именем пода (для наложения на график).
#
# Использование:
#   ./chaos.sh [интервал_сек]     (по умолчанию 5)
#   ./chaos.sh 10 | tee chaos.log
#
# Остановка: Ctrl-C.

set -uo pipefail

NAMESPACE="shop"
SELECTOR='app.kubernetes.io/name in (api,worker)'
INTERVAL="${1:-5}"

if ! [[ "$INTERVAL" =~ ^[0-9]+$ ]] || [ "$INTERVAL" -lt 1 ]; then
  echo "Интервал должен быть целым числом секунд >= 1, получено: '$INTERVAL'" >&2
  exit 1
fi

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl не найден в PATH" >&2
  exit 1
fi

timestamp() {
  date -Iseconds
}

trap 'echo "$(timestamp) остановлено"; exit 0' INT TERM

echo "$(timestamp) старт: namespace=$NAMESPACE selector='$SELECTOR' interval=${INTERVAL}s"

while true; do
  # Список кандидатов. Ошибка API не должна останавливать эксперимент — просто пропускаем итерацию.
  #
  # Под, которому уже проставлен deletionTimestamp, остаётся в выдаче всё время
  # graceful shutdown и по-прежнему числится в фазе Running, поэтому --field-selector
  # его не отсеивает. Без этого фильтра часть итераций уходила бы вхолостую:
  # «убивали» бы того, кто и так умирает, а живые реплики не трогали.
  pod=$(kubectl -n "$NAMESPACE" get pods -l "$SELECTOR" \
    -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}' \
    2>/dev/null | shuf -n1)

  if [ -z "$pod" ]; then
    echo "$(timestamp) кандидатов нет, пропуск"
  elif kubectl -n "$NAMESPACE" delete pod "$pod" --wait=false >/dev/null 2>&1; then
    echo "$(timestamp) $(date +%s) удалён pod/$pod"
  else
    echo "$(timestamp) не удалось удалить pod/$pod"
  fi

  sleep "$INTERVAL"
done
