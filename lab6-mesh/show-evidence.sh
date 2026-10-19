#!/usr/bin/env bash
# Показывает доказательства лабы 6 компактно, для скриншотов.
#
#   ./show-evidence.sh 1     шифрование и надёжность (Части 3-4)
#   ./show-evidence.sh 2     деление трафика и автопилот (Части 5-7)
#   ./show-evidence.sh 3     цена меша (Часть 8)
D="$(cd "$(dirname "$0")" && pwd)/docs"
B=$'\e[1m'; N=$'\e[0m'; G=$'\e[32m'; Y=$'\e[33m'

h() { printf '\n%s=== %s ===%s\n' "$B" "$1" "$N"; }

case "${1:-1}" in
1)
  h "Часть 3. Шифрование и идентичность"
  sed -n '3,12p' "$D/part3-mtls.txt"
  printf '%sСлева число запросов: из меша — tls=true с идентичностью сервисного\n' "$Y"
  printf 'аккаунта, снаружи меша — no_identity.%s\n' "$N"

  h "Часть 4. Надёжность силами меша"
  sed -n '/Замер 1/,/== Какой механизм/p' "$D/part4-retries.txt" | grep -vE '^#|^$'
  grep '^outbound_http_route_retry' "$D/part4-retries.txt" |
    sed -E 's/\{[^}]*\}//; s/_total//' |
    awk '{printf "  %-30s %s\n", $1, $2}'
  printf '%sretry_overflow = 0: размыкатель не вмешивался, сработали именно повторы.%s\n' "$G" "$N"
  ;;
2)
  h "Часть 5. Фактическое разделение трафика"
  sed -n '3,10p' "$D/part5-split.txt"

  h "Часть 7. Сценарий 1 — сломанная версия, автоматический откат"
  cat "$D/part7-rollback.txt"
  printf '%sГейт увидел долю ошибок 1.000 и вернул трафик на стабильную версию.%s\n' "$G" "$N"

  h "Часть 7. Сценарий 2 — здоровая версия, автоматический докат"
  cat "$D/part7-promote.txt"
  ;;
3)
  h "Часть 8. Цена меша"
  cat "$D/part8-cost.txt"
  ;;
esac
echo
