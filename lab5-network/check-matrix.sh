#!/usr/bin/env bash
# Живая проверка матрицы доступа (Часть 5 лабы 5).
# Каждая строка матрицы — отдельное НОВОЕ соединение с таймаутом: результат
# не зависит от того, что было открыто до применения политик.
# Скрипт сам сверяет факт с ожиданием из lab5-network/matrix.md и печатает
# итог. Зелёный столбец справа — и есть «зелёная матрица».
#
#   ./check-matrix.sh          основные строки (быстро, ~40 секунд)
#   ./check-matrix.sh --full   плюс исходящие потоки api/worker/batch
#                              через эфемерные контейнеры (~1.5 минуты)
#   ./check-matrix.sh -q       без показа команд
set -uo pipefail

NS=shop
IMG=ghcr.io/nicolaka/netshoot:v0.13
FULL=0; QUIET=0
for a in "$@"; do
  case "$a" in
    --full) FULL=1 ;;
    -q|--quiet) QUIET=1 ;;
  esac
done

if [ -t 1 ]; then
  G=$'\e[32m'; R=$'\e[31m'; D=$'\e[2m'; B=$'\e[1m'; N=$'\e[0m'
else
  G=""; R=""; D=""; B=""; N=""
fi

pass=0; mismatch=0; n=0

# printf с %-Ns дополняет по байтам, поэтому строки с кириллицей уезжают.
# ${#s} в bash при UTF-8 локали считает символы — дополняем вручную.
pad() {
  local s="$1" w="$2" l
  l=${#s}
  printf '%s' "$s"
  while [ "$l" -lt "$w" ]; do printf ' '; l=$((l + 1)); done
}

row() { # row <описание> <ожидание> <факт> <вердикт>
  printf '  '; pad "$1" 34
  printf ' ждём '; pad "$2" 10
  printf ' факт '; pad "$3" 10
  printf ' %b\n' "$4"
}

tally() { # tally <want> <got> -> печатает вердикт в $VERDICT
  if [ "$1" = "$2" ]; then VERDICT="${G}совпало${N}"; pass=$((pass + 1))
  else VERDICT="${R}РАСХОЖДЕНИЕ${N}"; mismatch=$((mismatch + 1)); fi
}

txt() { [ "$1" = allow ] && echo "разрешено" || echo "запрещено"; }

# check <описание> <allow|deny> <команда...>
check() {
  local desc="$1" want="$2"; shift 2
  n=$((n + 1))
  [ "$QUIET" -eq 1 ] || printf '%s$ %s%s\n' "$D" "$*" "$N"
  local got
  if timeout 20 "$@" >/dev/null 2>&1; then got=allow; else got=deny; fi
  tally "$want" "$got"
  row "$desc" "$(txt "$want")" "$(txt "$got")" "$VERDICT"
}

hdr() { printf '\n%s== %s ==%s\n' "$B" "$1" "$N"; }

printf '%sПроверка матрицы доступа shop · %s%s\n' "$B" "$(date +%H:%M:%S)" "$N"

hdr "client — легальный клиент"
check "client → api:8080"        allow kubectl -n $NS exec client -- nc -zvw3 shop-api 8080
check "client → worker:8081"     deny  kubectl -n $NS exec client -- nc -zvw3 shop-worker 8081
check "client → batch:8080"      deny  kubectl -n $NS exec client -- nc -zvw3 shop-batch 8080
check "client → postgres:5432"   deny  kubectl -n $NS exec client -- nc -zvw3 shop-db-rw 5432
check "client → DNS"             allow kubectl -n $NS exec client -- nslookup shop-api
check "client → внешний мир"     deny  kubectl -n $NS exec client -- nc -zvw3 1.1.1.1 443

hdr "intruder — посторонний под"
check "intruder → api:8080"      deny  kubectl -n $NS exec intruder -- nc -zvw3 shop-api 8080
check "intruder → worker:8081"   deny  kubectl -n $NS exec intruder -- nc -zvw3 shop-worker 8081
check "intruder → batch:8080"    deny  kubectl -n $NS exec intruder -- nc -zvw3 shop-batch 8080
check "intruder → postgres:5432" deny  kubectl -n $NS exec intruder -- nc -zvw3 shop-db-rw 5432
check "intruder → DNS"           allow kubectl -n $NS exec intruder -- nslookup shop-api

hdr "обход через NodePort — гипотеза из матрицы"
check "intruder → своя нода:30006"  deny kubectl -n $NS exec intruder -- nc -zvw3 172.18.0.2 30006
check "intruder → чужая нода:30006" deny kubectl -n $NS exec intruder -- nc -zvw3 172.18.0.3 30006

if [ "$FULL" -eq 1 ]; then
  # В образах api/worker/batch нет шелла, поэтому исходящие потоки проверяем
  # эфемерным контейнером в сетевом namespace того же пода: identity в Cilium
  # принадлежит поду, значит трафик идёт от его имени.
  hdr "исходящие потоки api / worker / batch (эфемерные контейнеры)"
  ec() { # ec <описание> <allow|deny> <под> <целевой контейнер> <имя> <команда>
    local desc="$1" want="$2" pod="$3" tgt="$4" name="$5" cmd="$6"
    n=$((n + 1))
    [ "$QUIET" -eq 1 ] || printf '%s$ kubectl debug %s --target=%s -- %s%s\n' "$D" "$pod" "$tgt" "$cmd" "$N"
    kubectl -n $NS debug "$pod" --image="$IMG" --target="$tgt" -c "$name" -- sh -c "$cmd" >/dev/null 2>&1
    local got=deny i=0 log=""
    while [ $i -lt 8 ]; do
      log=$(kubectl -n $NS logs "$pod" -c "$name" 2>/dev/null)
      [ -n "$log" ] && break
      i=$((i + 1)); sleep 3
    done
    echo "$log" | grep -qi "succeeded" && got=allow
    tally "$want" "$got"
    row "$desc" "$(txt "$want")" "$(txt "$got")" "$VERDICT"
  }
  API=$(kubectl -n $NS get pod -l app.kubernetes.io/name=api -o jsonpath='{.items[0].metadata.name}')
  WRK=$(kubectl -n $NS get pod -l app.kubernetes.io/name=worker -o jsonpath='{.items[0].metadata.name}')
  BAT=$(kubectl -n $NS get pod -l app.kubernetes.io/name=batch -o jsonpath='{.items[0].metadata.name}')
  S=$(date +%s)
  ec "api → postgres:5432"  allow "$API" api    "m$S-1" 'nc -zvw3 shop-db-rw 5432'
  ec "api → worker:8081"    deny  "$API" api    "m$S-2" 'nc -zvw3 shop-worker 8081'
  ec "worker → postgres"    allow "$WRK" worker "m$S-3" 'nc -zvw3 shop-db-rw 5432'
  ec "batch → postgres"     deny  "$BAT" batch  "m$S-4" 'nc -zvw3 shop-db-rw 5432'
fi

hdr "вердикты датаплейна"
# Таймаут сам по себе не доказывает работу политики: его даёт и отсутствие
# маршрута. Hubble называет причину.
CIL=$(kubectl -n kube-system get pod -l k8s-app=cilium -o name | head -1)
kubectl -n kube-system exec "$CIL" -c cilium-agent -- \
  hubble observe --since 3m --verdict DROPPED --namespace $NS 2>/dev/null |
  grep "Policy denied" | tail -5 | sed 's/^/  /'

printf '\n%sИтог: %d из %d совпало с матрицей' "$B" "$pass" "$n"
if [ "$mismatch" -eq 0 ]; then
  printf ' — %sматрица зелёная%s\n' "$G" "$N"
else
  printf ' — %sрасхождений: %d%s\n' "$R" "$mismatch" "$N"
fi
exit $mismatch
