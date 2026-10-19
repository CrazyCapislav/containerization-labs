#!/usr/bin/env bash
# Гейт канареечного релиза (Часть 6 лабы 6).
#
# Раз в заданный интервал смотрит на две метрики версии v2 — долю ошибок
# и задержку — и сам двигает разделение трафика: докатывает здоровую
# версию до 100% или откатывает сломанную в 0. Человек в цикле не нужен.
#
#   ./gate.sh [интервал_сек] [шаг_%]      по умолчанию 15 и 15
#
# Правило намеренно простое, потому что его цель — быть объяснимым:
#
#   трафика меньше MIN_RPS        → решение отложено, данных нет
#   доля ошибок v2 > ERR_LIMIT    → немедленный откат в 0, выход
#   задержка v2 p95 > LAT_LIMIT   → немедленный откат в 0, выход
#   доля достигла 100             → релиз докачен, выход
#   иначе                         → поднять долю v2 на шаг
#
# Почему веса правятся патчем HTTPRoute, а не helm upgrade: гейт работает
# в цикле каждые несколько секунд, а upgrade — перерисовка релиза целиком.
# Объект маршрута принадлежит чарту, гейт меняет в нём ровно одно поле.
set -uo pipefail

INTERVAL="${1:-15}"
STEP="${2:-15}"
NS=shop
ROUTE=shop-api-split
PROM=http://localhost:30001
ERR_LIMIT=0.05      # 5% ошибок — порог отката
LAT_LIMIT=1.0       # секунда на 95-м перцентиле — порог отката
MIN_RPS=0.2         # ниже этого считаем, что данных для решения нет

say() { printf '%s  %s\n' "$(date +%H:%M:%S)" "$*"; }

prom() {
  curl -s -G "$PROM/api/v1/query" --data-urlencode "query=$1" |
    jq -r '.data.result[0].value[1] // empty'
}

weights() {
  kubectl -n $NS get httproute $ROUTE \
    -o jsonpath='{.spec.rules[0].backendRefs[0].weight} {.spec.rules[0].backendRefs[1].weight}'
}

set_weights() { # set_weights <стабильная> <канарейка>
  kubectl -n $NS patch httproute $ROUTE --type=json -p "[
    {\"op\":\"replace\",\"path\":\"/spec/rules/0/backendRefs/0/weight\",\"value\":$1},
    {\"op\":\"replace\",\"path\":\"/spec/rules/0/backendRefs/1/weight\",\"value\":$2}]" >/dev/null
}

say "гейт запущен: интервал ${INTERVAL}с, шаг ${STEP}%, порог ошибок ${ERR_LIMIT}, порог задержки ${LAT_LIMIT}с"

while true; do
  read -r STABLE CANARY <<<"$(weights)"
  if [ -z "${CANARY:-}" ]; then
    say "не читается HTTPRoute $ROUTE, выход"
    exit 1
  fi

  RPS=$(prom 'sum(rate(http_requests_total{namespace="shop",version="v2",path="/order"}[1m]))')
  ERR=$(prom 'sum(rate(http_requests_total{namespace="shop",version="v2",path="/order",status=~"5.."}[1m])) / clamp_min(sum(rate(http_requests_total{namespace="shop",version="v2",path="/order"}[1m])), 0.001)')
  LAT=$(prom 'histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{namespace="shop",version="v2",path="/order"}[1m])))')

  RPS=${RPS:-0}; ERR=${ERR:-0}; LAT=${LAT:-0}
  SUM=$(printf 'доля_v2=%s%%  ошибки=%.3f  p95=%.3fс  rps=%.2f' "$CANARY" "$ERR" "$LAT" "$RPS")

  # Решение принимается только по свежим данным: без трафика доля ошибок
  # равна нулю не потому, что всё хорошо, а потому, что мерить нечего.
  # Повышать долю на таком основании нельзя.
  if awk "BEGIN{exit !($RPS < $MIN_RPS)}"; then
    say "$SUM — трафика мало, решение отложено"
  elif awk "BEGIN{exit !($ERR > $ERR_LIMIT)}"; then
    say "$SUM — ОТКАТ: доля ошибок выше порога $ERR_LIMIT"
    set_weights 100 0
    say "трафик возвращён на стабильную версию, гейт остановлен"
    exit 2
  elif awk "BEGIN{exit !($LAT > $LAT_LIMIT)}"; then
    say "$SUM — ОТКАТ: задержка выше порога ${LAT_LIMIT}с"
    set_weights 100 0
    say "трафик возвращён на стабильную версию, гейт остановлен"
    exit 2
  elif [ "$CANARY" -ge 100 ]; then
    say "$SUM — релиз докачен до 100%, гейт остановлен"
    exit 0
  else
    NEW=$(( CANARY + STEP )); [ "$NEW" -gt 100 ] && NEW=100
    set_weights $(( 100 - NEW )) "$NEW"
    say "$SUM — здорова, доля поднята до ${NEW}%"
  fi

  sleep "$INTERVAL"
done
