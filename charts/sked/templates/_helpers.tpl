{{- define "sked.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sked.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 40 | trimSuffix "-" -}}
{{- else if contains (include "sked.name" .) .Release.Name -}}
{{- .Release.Name | trunc 40 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "sked.name" .) | trunc 40 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "sked.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sked.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end -}}

{{- define "sked.labels" -}}
{{ include "sked.selectorLabels" . }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "sked.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (printf "%s-controller-manager" (include "sked.fullname" .)) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "sked.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{- define "sked.trustPolicyName" -}}
{{- default (printf "%s-scheduler-trust-policy" (include "sked.fullname" .)) .Values.trustPolicy.existingConfigMap -}}
{{- end -}}

{{- define "sked.trustPolicy" -}}
{{- if kindIs "map" .Values.trustPolicy.policy -}}
{{- toYaml .Values.trustPolicy.policy -}}
{{- else -}}
{{- .Files.Get "files/trust-policy.yaml" -}}
{{- end -}}
{{- end -}}

{{- define "sked.webhookSecretName" -}}
{{- if .Values.webhook.certManager.enabled -}}
{{- default (printf "%s-webhook-server-cert" (include "sked.fullname" .)) .Values.webhook.secretName -}}
{{- else -}}
{{- required "webhook.secretName is required when webhook.certManager.enabled=false" .Values.webhook.secretName -}}
{{- end -}}
{{- end -}}
