{{/*
Expand the name of the chart.
*/}}
{{- define "valkey-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "valkey-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "valkey-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "valkey-operator.labels" -}}
helm.sh/chart: {{ include "valkey-operator.chart" . }}
{{ include "valkey-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "valkey-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "valkey-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "valkey-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "valkey-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The operator image reference. With image.digest set it is repository:tag@digest:
the runtime pulls by the digest, and the tag stays for the reader. The same
reference goes to --operator-image, so the sidecar and the observer the operator
generates run the pinned image too.
*/}}
{{- define "valkey-operator.image" -}}
{{- $ref := printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- with .Values.image.digest }}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" .) }}
{{- fail (printf "image.digest must be sha256:<64 hex characters>, got %q" .) }}
{{- end }}
{{- $ref = printf "%s@%s" $ref . }}
{{- end }}
{{- $ref }}
{{- end }}

{{/*
The pod-level hardening shared by the operator Deployment and the pre-upgrade
hook Job (docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md,
D6): the distroless nonroot identity, the seccomp profile - RuntimeDefault or
Localhost, never Unconfined - and the opt-in user namespace. Both pods talk to the
API server, so the token stays mounted, stated rather than defaulted.
*/}}
{{- define "valkey-operator.podHardening" -}}
automountServiceAccountToken: true
enableServiceLinks: false
{{- if .Values.podSecurity.userNamespaces }}
hostUsers: false
{{- end }}
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    {{- $sp := .Values.podSecurity.seccompProfile }}
    {{- if eq $sp.type "RuntimeDefault" }}
    {{- if $sp.localhostProfile }}
    {{- fail "podSecurity.seccompProfile.localhostProfile is only valid with type Localhost" }}
    {{- end }}
    type: RuntimeDefault
    {{- else if eq $sp.type "Localhost" }}
    {{- if not $sp.localhostProfile }}
    {{- fail "podSecurity.seccompProfile.localhostProfile is required with type Localhost" }}
    {{- end }}
    {{- if or (hasPrefix "/" $sp.localhostProfile) (regexMatch "(^|/)[.][.](/|$)" $sp.localhostProfile) }}
    {{- fail (printf "podSecurity.seccompProfile.localhostProfile %q must be a relative path without '..'" $sp.localhostProfile) }}
    {{- end }}
    type: Localhost
    localhostProfile: {{ $sp.localhostProfile | quote }}
    {{- else }}
    {{- fail (printf "podSecurity.seccompProfile.type must be RuntimeDefault or Localhost, got %q" $sp.type) }}
    {{- end }}
{{- end }}

{{/*
The container-level posture of the operator and hook containers.
*/}}
{{- define "valkey-operator.containerSecurityContext" -}}
securityContext:
  privileged: false
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop:
      - ALL
{{- end }}

{{/*
The --allowed-seccomp-localhost-profiles value: the listed paths joined by commas.
Each entry must be a relative path without a ".." element, like the CRD demands of
localhostProfile, and must not contain the separator.
*/}}
{{- define "valkey-operator.allowedSeccompLocalhostProfiles" -}}
{{- range .Values.valkeyPodSecurity.allowedSeccompLocalhostProfiles }}
{{- if or (not .) (hasPrefix "/" .) (contains "," .) (regexMatch "(^|/)[.][.](/|$)" .) }}
{{- fail (printf "valkeyPodSecurity.allowedSeccompLocalhostProfiles: %q must be a non-empty relative path without '..' or ','" .) }}
{{- end }}
{{- end }}
{{- join "," .Values.valkeyPodSecurity.allowedSeccompLocalhostProfiles }}
{{- end }}
