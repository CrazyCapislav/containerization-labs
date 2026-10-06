{{/* Метки, обязательные по политике require-labels, плюс стандартные рекомендованные. */}}
{{- define "shop.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/part-of: {{ .partOf }}
app.kubernetes.io/instance: {{ .release }}
app.kubernetes.io/managed-by: Helm
{{- end -}}

{{- define "shop.selectorLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .release }}
{{- end -}}
