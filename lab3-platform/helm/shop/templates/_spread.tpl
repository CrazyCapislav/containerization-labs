
{{/*
  Разнесение реплик по топологическим доменам (Часть 1 лабы 4).
  Чарт умеет оба механизма, выбор делает spread.mode. Они решают разные
  задачи: topologySpread удерживает перекос между доменами в пределах
  maxSkew, antiAffinity просто запрещает соседство и понятия равномерности
  не имеет. Обоснование дефолта — в README лабы 4.
  Параметры: name, release, spread.
*/}}
{{- define "shop.spread" -}}
{{- if eq .spread.mode "topologySpread" -}}
topologySpreadConstraints:
  - maxSkew: {{ .spread.maxSkew }}
    topologyKey: {{ .spread.topologyKey }}
    whenUnsatisfiable: {{ .spread.whenUnsatisfiable }}
    # Считаем только поды своей ревизии. Без этого во время выкатки в
    # счётчик попадают и старые поды, и новому может не хватить домена.
    matchLabelKeys:
      - pod-template-hash
    labelSelector:
      matchLabels:
        {{- include "shop.selectorLabels" (dict "name" .name "release" .release) | nindent 8 }}
{{- else if eq .spread.mode "antiAffinity" -}}
affinity:
  podAntiAffinity:
    {{- if eq .spread.strictness "required" }}
    # Жёсткий запрет соседства: на домен попадает максимум один такой под.
    # Если реплик больше, чем доменов, лишние останутся в Pending.
    requiredDuringSchedulingIgnoredDuringExecution:
      - topologyKey: {{ .spread.topologyKey }}
        labelSelector:
          matchLabels:
            {{- include "shop.selectorLabels" (dict "name" .name "release" .release) | nindent 12 }}
    {{- else }}
    # Мягкий вариант: планировщик учитывает правило при оценке нод,
    # но при нехватке места поставит реплики рядом.
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: {{ .spread.topologyKey }}
          labelSelector:
            matchLabels:
              {{- include "shop.selectorLabels" (dict "name" .name "release" .release) | nindent 14 }}
    {{- end }}
{{- end -}}
{{- end -}}
