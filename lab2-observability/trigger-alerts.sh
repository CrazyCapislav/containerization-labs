#!/usr/bin/env bash
# Провоцирует срабатывание алертов.
#   ошибки   — поток запросов к /fail: доля 5xx уходит далеко за 5%
#   задержка — параллельные /load с большим n: ручки упираются в лимит CPU
# Запуск: ./trigger-alerts.sh [секунд]   (по умолчанию 600)
set -u
HOST=${HOST:-localhost:30003}
DURATION=${1:-600}
END=$(( $(date +%s) + DURATION ))

echo "давлю на $HOST в течение $DURATION с"
while [ "$(date +%s)" -lt "$END" ]; do
  for _ in 1 2 3 4 5; do curl -s -o /dev/null "http://$HOST/fail" & done
  for _ in 1 2 3; do curl -s -o /dev/null "http://$HOST/load?n=200" & done
  curl -s -o /dev/null "http://$HOST/health"
  sleep 1
done
wait
echo "нагрузка завершена"
