#!/usr/bin/env bash
# Фоновая нагрузка на сервис: нужна, чтобы графики RED не были плоскими.
# Запуск: ./load.sh [секунд]   (по умолчанию 600)
set -u
HOST=${HOST:-localhost:30003}
DURATION=${1:-600}
END=$(( $(date +%s) + DURATION ))

echo "нагрузка на $HOST в течение $DURATION с, Ctrl+C для остановки"
while [ "$(date +%s)" -lt "$END" ]; do
  curl -s "http://$HOST/load?n=40" >/dev/null &
  curl -s -o /dev/null "http://$HOST/fail"
  curl -s -o /dev/null "http://$HOST/slow" &
  curl -s -o /dev/null "http://$HOST/health"
  sleep 2
done
wait
echo "нагрузка завершена"
