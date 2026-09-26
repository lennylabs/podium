{{- define "podium.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "podium.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "podium.labels" -}}
app.kubernetes.io/name: {{ include "podium.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "podium.selectorLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "podium.pgFullname" -}}
{{- if .Values.postgresql.fullnameOverride -}}
{{- .Values.postgresql.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-pg" (include "podium.fullname" . | trunc 60 | trimSuffix "-") -}}
{{- end -}}
{{- end -}}

{{- define "podium.pgSelectorLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 60 | trimSuffix "-" }}-pg
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "podium.pgLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 60 | trimSuffix "-" }}-pg
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: postgresql
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/*
The migrate Job's name. The suffix is appended after truncating the base, so a
63-character fullnameOverride still yields a name that ends in -migrate and fits
the 63-character limit.
*/}}
{{- define "podium.migrateFullname" -}}
{{- printf "%s-migrate" (include "podium.fullname" . | trunc 55 | trimSuffix "-") -}}
{{- end -}}

{{/*
Labels for the migrate Job and its pod. The name value differs from the
selector's, so the Service never routes to the Job pod, and neither the stop
gate nor the documented `kubectl wait` matches it. The component label is what
the documented cleanup selector matches.
*/}}
{{- define "podium.migrateLabels" -}}
app.kubernetes.io/name: {{ include "podium.name" . | trunc 55 | trimSuffix "-" }}-migrate
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: migrate
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/*
The registry image reference, shared by the Deployment and the migrate Job so
the image gate compares the reference both would run.
*/}}
{{- define "podium.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion | toString) -}}
{{- end -}}

{{/*
The §13.4 stored-value migration this chart release requires the store to have
completed. A future release that names a new stored-value migration changes the
value, and the serving preflight then fails until that migration runs.
*/}}
{{- define "podium.storedRowFormat" -}}
content_hash_framing
{{- end -}}

{{/*
The live Deployment of this release, or an empty map under `helm template` and
on a first install.
*/}}
{{- define "podium.liveDeployment" -}}
{{- toJson (lookup "apps/v1" "Deployment" .Release.Namespace (include "podium.fullname" .)) -}}
{{- end -}}

{{/*
The migrate Job, or an empty map. A hook Job is outside the release manifest and
survives until the next hook run replaces it, so the gates read its status and
its record annotations.
*/}}
{{- define "podium.liveMigrateJob" -}}
{{- toJson (lookup "batch/v1" "Job" .Release.Namespace (include "podium.migrateFullname" .)) -}}
{{- end -}}

{{/*
The live Deployment's UID, or "". The migrate Job records it, and the gates
accept only a Job that recorded the UID of the Deployment that is live now. A
hook Job survives `helm uninstall`, and a reinstall restarts the release
revision at 1, so the revision alone cannot tell a Job of this release instance
from one an earlier instance left.
*/}}
{{- define "podium.liveDeploymentUID" -}}
{{- toString (dig "metadata" "uid" "" (include "podium.liveDeployment" . | fromJson)) -}}
{{- end -}}

{{/*
A digest of the pod configuration the migrate Job reads the store, the object
store, and the signing key through: the Secret reference, the environment, the
mounts, the volumes, and the version of every Secret the pod reads. The dry-run
Job records it, and the reviewed-dry-run gate refuses a run whose configuration
differs from the reviewed one, because the run would then read, and attest,
rows the dry run never listed.

The rendered blocks carry Secret names only. With an external database the DSN
reaches the pod through existingSecret, and the signing key is the content of
signing.secretName, so an edit inside a Secret changes the store or the key
without changing the render. Each referenced Secret's uid and resourceVersion
therefore enter the digest: an edit changes the resourceVersion, and a delete
and recreate changes the uid. A write that leaves the data unchanged, such as a
label edit, also changes the resourceVersion and sends the operator back to the
dry run, which fails closed. Under `helm template` the lookup returns nothing
on both sides, so the digest stays consistent there.
*/}}
{{- define "podium.podConfigDigest" -}}
{{- $versions := "" -}}
{{- range (include "podium.referencedSecrets" . | fromJsonArray) -}}
{{- $secret := lookup "v1" "Secret" $.Release.Namespace . -}}
{{- $versions = printf "%s\n%s:%s:%s" $versions . (toString (dig "metadata" "uid" "" $secret)) (toString (dig "metadata" "resourceVersion" "" $secret)) -}}
{{- end -}}
{{- printf "%s%s%s%s%s" (include "podium.envFrom" .) (include "podium.env" .) (include "podium.volumeMounts" .) (include "podium.volumes" .) $versions | sha256sum -}}
{{- end -}}

{{/*
The names of the Secrets the registry pod and the migrate Job read, sorted and
without duplicates, as a JSON array: existingSecret, the signing key Secret,
the runtime-keys Secret, the bundled database's password Secret, and every
Secret an extraEnv entry reads through secretKeyRef.
*/}}
{{- define "podium.referencedSecrets" -}}
{{- $names := list -}}
{{- with .Values.existingSecret -}}{{- $names = append $names . -}}{{- end -}}
{{- if eq .Values.signing.mode "registry-key" -}}
{{- with .Values.signing.secretName -}}{{- $names = append $names . -}}{{- end -}}
{{- end -}}
{{- if .Values.runtimeKeys.enabled -}}
{{- with .Values.runtimeKeys.secretName -}}{{- $names = append $names . -}}{{- end -}}
{{- end -}}
{{- if .Values.postgresql.enabled -}}
{{- with .Values.postgresql.existingSecret -}}{{- $names = append $names . -}}{{- end -}}
{{- end -}}
{{- range (.Values.extraEnv | default list) -}}
{{- with (dig "valueFrom" "secretKeyRef" "name" "" .) -}}{{- $names = append $names . -}}{{- end -}}
{{- end -}}
{{- $names | uniq | sortAlpha | toJson -}}
{{- end -}}

{{/*
The image the live Deployment's registry container runs, or "".
*/}}
{{- define "podium.liveImage" -}}
{{- $live := include "podium.liveDeployment" . | fromJson -}}
{{- $image := "" -}}
{{- range (dig "spec" "template" "spec" "containers" (list) $live) -}}
{{- if eq (get . "name") "podium-server" -}}
{{- $image = get . "image" -}}
{{- end -}}
{{- end -}}
{{- $image -}}
{{- end -}}

{{/*
Whether the serving preflight runs on this render: the lookup gates are on, the
render serves (replicaCount above zero), and the pod keeps its probes. The
signing-off unprobedStart path runs the rewrite at boot and skips it.
*/}}
{{- define "podium.servingPreflight" -}}
{{- if and .Values.migration.preflight (gt (int .Values.replicaCount) 0) (not .Values.migration.unprobedStart) (eq (toString .Values.migration.mode) "disabled") -}}
true
{{- end -}}
{{- end -}}

{{/*
The Deployment's metadata annotations that record the migration state. Neither
renders on the pod template, so neither causes a rollout.

stored-row-format names the §13.4 migration the store has completed. It renders
on a serving render whose preflight passed, which on an upgrade that finds no
live Deployment requires migration.storeReady; when the live Deployment already
carries it; on a serving render that disables the preflight outside the
unprobedStart path, where preflight=false is the operator's completion
assertion; and on an install that states migration.storeReady. A zero-replica
render with preflight=false does not write it, because the procedure's
zero-replica step precedes the migrate Job. It never renders on an
unprobedStart render, and a migration render keeps only a value the live
Deployment already carries.

pre-migration-image records the image the Deployment ran before this migration,
which the image gate compares the migrate Job's image against. It renders only
while the live Deployment lacks stored-row-format. A recorded value is kept.
Otherwise the value is the live registry image when it differs from the
rendered one, and on an install, which has no live Deployment to read, it is
migration.previousImage, which podium.validate requires on an install over an
existing store.

Spec: §13.4
*/}}
{{- define "podium.migrationAnnotations" -}}
{{- $live := include "podium.liveDeployment" . | fromJson -}}
{{- $liveAnn := dig "metadata" "annotations" (dict) $live -}}
{{- $format := include "podium.storedRowFormat" . -}}
{{- $liveFormat := get $liveAnn "podium.lennylabs.dev/stored-row-format" -}}
{{- $mode := toString .Values.migration.mode -}}
{{- $writeFormat := false -}}
{{- if .Values.migration.unprobedStart -}}
{{- $writeFormat = false -}}
{{- else if ne $mode "disabled" -}}
{{- $writeFormat = eq $liveFormat $format -}}
{{- else if include "podium.servingPreflight" . -}}
{{- $writeFormat = true -}}
{{- else if eq $liveFormat $format -}}
{{- $writeFormat = true -}}
{{- else if and (not .Values.migration.preflight) (gt (int .Values.replicaCount) 0) -}}
{{- $writeFormat = true -}}
{{- else if and .Release.IsInstall .Values.migration.storeReady -}}
{{- $writeFormat = true -}}
{{- end -}}
{{- if $writeFormat }}
podium.lennylabs.dev/stored-row-format: {{ $format }}
{{- end -}}
{{- if ne $liveFormat $format -}}
{{- $recorded := get $liveAnn "podium.lennylabs.dev/pre-migration-image" -}}
{{- $liveImage := include "podium.liveImage" . -}}
{{- if $recorded }}
podium.lennylabs.dev/pre-migration-image: {{ $recorded | quote }}
{{- else if not $live }}
{{- with .Values.migration.previousImage }}
podium.lennylabs.dev/pre-migration-image: {{ . | quote }}
{{- end }}
{{- else if and $liveImage (ne $liveImage (include "podium.image" .)) }}
podium.lennylabs.dev/pre-migration-image: {{ $liveImage | quote }}
{{- end -}}
{{- end -}}
{{- end -}}
