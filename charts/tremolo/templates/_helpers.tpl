{{- define "tremolo.name" -}}tremolo{{- end }}

{{- define "tremolo.labels" -}}
app.kubernetes.io/name: tremolo
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "tremolo.selector" -}}
app.kubernetes.io/name: tremolo
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
