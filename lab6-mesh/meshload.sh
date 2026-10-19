#!/usr/bin/env bash
# Непрерывная нагрузка изнутри меша (Части 6-7 лабы 6).
# Гейт принимает решения по метрикам, а метрики берутся из трафика:
# без нагрузки он честно отложит решение и ничего не докатит.
#
#   ./meshload.sh [минут]     по умолчанию 10
MIN="${1:-10}"
kubectl -n shop exec client -c probe -- sh -c "
  end=\$(( \$(date +%s) + $MIN * 60 ))
  while [ \$(date +%s) -lt \$end ]; do
    curl -s -o /dev/null --max-time 5 -X POST http://shop-api:8080/order
    curl -s -o /dev/null --max-time 5 http://shop-api:8080/version
  done
"
