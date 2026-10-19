#!/usr/bin/env bash
# Замер задержки и потребления api (Части 2 и 8 лабы 6).
#
# Процедура одна на оба замера — до меша и с мешем. Иначе сравнение
# превратится в сравнение двух разных методик, а не двух состояний стенда.
#
#   ./measure.sh baseline     замер без меша (Часть 2)
#   ./measure.sh mesh         замер с мешем  (Часть 8)
#
# Пишет docs/measure-<метка>.txt: пороги и перцентили k6 плюс потребление
# каждого контейнера пода api за то же окно, что и нагрузка.
set -uo pipefail

LABEL="${1:-baseline}"
MIN="${2:-3}"
R="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$R/lab6-mesh/docs/measure-${LABEL}.txt"
PROM=http://localhost:30001
mkdir -p "$(dirname "$OUT")"

echo "Замер «$LABEL», $(date -Iseconds), нагрузка ${MIN} мин" | tee "$OUT"

START=$(date +%s)
# Длительность задаётся переменной окружения, а не флагом --duration:
# флаг CLI отбрасывает сценарий constant-arrival-rate целиком и подменяет
# его закрытой моделью, которая при замедлении сама снижает нагрузку
# и прячет деградацию. Для замера задержки это недопустимо.
DURATION="${MIN}m" BASE_URL=http://localhost:30006 \
  k6 run "$R/lab5-network/load.js" 2>&1 | sed -n '/THRESHOLDS/,/EXECUTION/p' | tee -a "$OUT"
END=$(date +%s)
W=$(( END - START ))

q() { # q <описание> <promql>
  local v
  v=$(curl -s -G "$PROM/api/v1/query" --data-urlencode "query=$2" |
      jq -r '.data.result[]? | [(.metric.container // "все"), .value[1]] | @tsv')
  printf '%s\n%s\n' "$1" "${v:-  нет данных}"
}

{
  echo
  echo "== Потребление подов api за окно нагрузки (${W}с) =="
  echo "# Разбивка по контейнерам: с мешем в поде появляется второй, и честная"
  echo "# цена складывается из обоих, а не только из приложения."
  echo
  q "CPU, ядер в среднем за окно (контейнер / значение):" \
     "sum by (container) (avg_over_time(rate(container_cpu_usage_seconds_total{namespace=\"shop\",pod=~\"shop-api-.*\",container!=\"\"}[1m])[${W}s:15s]))"
  echo
  q "Память, байт (максимум рабочего набора за окно):" \
     "sum by (container) (max_over_time(container_memory_working_set_bytes{namespace=\"shop\",pod=~\"shop-api-.*\",container!=\"\"}[${W}s]))"
  echo
  q "Задержка p95 по серверной гистограмме, секунд:" \
     "histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{namespace=\"shop\",path=\"/order\"}[${W}s])))"
  echo
  echo "== Состав подов api =="
  kubectl -n shop get pods -l app.kubernetes.io/name=api \
    -o jsonpath='{range .items[*]}{.metadata.name}{"  контейнеров: "}{range .spec.containers[*]}{.name}{" "}{end}{"\n"}{end}'
} | tee -a "$OUT"

echo
echo "Записано: $OUT"
