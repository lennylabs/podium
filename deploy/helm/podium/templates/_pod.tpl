{{/*
Pod-spec pieces the registry Deployment and the migrate Job share. The Job runs
`podium-server sign-stored-rows` against the same store, object store, and
signing key the Deployment serves from, so both pods take their image,
configuration environment, Secret references, and mounts from these defines
rather than from two copies that could drift apart.
*/}}

{{/*
Render guards. deployment.yaml and migrate-job.yaml both include this, and every
guard fails the render before anything reaches the cluster.

The pure-value guards run under `helm template`. The live-state guards read the
cluster through `lookup`, which returns nothing under `helm template`; the image
and reviewed-dry-run gates then fail closed, and a serving upgrade render
fails unless migration.storeReady=true, because it sees no Deployment.

Spec: §13.4
*/}}
{{- define "podium.validate" -}}
{{- if and (eq .Values.config.objectStore.type "filesystem") (not .Values.objects.enabled) }}
{{- fail "config.objectStore.type=filesystem requires objects.enabled=true: the object root would otherwise land on the read-only container root filesystem, where the registry logs one warning, disables the object store, and still reports ready" }}
{{- end }}
{{- if not (has .Values.signing.mode (list "registry-key" "none")) }}
{{- fail (printf "signing.mode is %q; it takes registry-key or none" (toString .Values.signing.mode)) }}
{{- end }}
{{- $mode := toString .Values.migration.mode }}
{{- $replicas := int .Values.replicaCount }}
{{- if not (has $mode (list "disabled" "dry-run" "run")) }}
{{- fail (printf "migration.mode is %q; it takes disabled, dry-run, or run" $mode) }}
{{- end }}
{{- $migrating := ne $mode "disabled" }}
{{- if and $migrating (ne $replicas 0) }}
{{- fail (printf "migration.mode=%s requires replicaCount=0, and replicaCount is %d: the §13.4 rewrite requires every registry process stopped; follow the upgrade procedure in docs/deployment/clustered.md" $mode $replicas) }}
{{- end }}
{{- if and $migrating (ne .Values.signing.mode "registry-key") }}
{{- fail (printf "migration.mode=%s requires signing.mode=registry-key: sign-stored-rows refuses with signing off (config.signature_provider_unavailable); a signing-off deployment runs the rewrite at boot with migration.unprobedStart=true" $mode) }}
{{- end }}
{{- if and (eq $mode "run") (not .Values.migration.reviewedDryRun) }}
{{- fail (printf "migration.mode=run requires migration.reviewedDryRun: run the migration.mode=dry-run step first, review its log, and pass the dry-run Job's UID, which `kubectl get job %s -o jsonpath='{.metadata.uid}'` prints" (include "podium.migrateFullname" .)) }}
{{- end }}
{{- if and $migrating (not .Release.IsUpgrade) }}
{{- fail (printf "migration.mode=%s runs only on helm upgrade: an install over an existing store installs at replicaCount=0 with no migration.mode first, then follows the upgrade procedure in docs/deployment/clustered.md" $mode) }}
{{- end }}
{{- if .Values.migration.unprobedStart }}
{{- if or (ne $replicas 1) (ne (toString .Values.strategy.type) "Recreate") $migrating (ne .Values.signing.mode "none") }}
{{- fail (printf "migration.unprobedStart=true requires signing.mode=none, replicaCount=1, strategy.type=Recreate, and migration.mode=disabled; this render has signing.mode=%s, replicaCount=%d, strategy.type=%s, and migration.mode=%s. A signing-on deployment runs the rewrite in the migrate Job with migration.mode=dry-run and then run, because the boot rewrite of a Postgres store leaves every unsigned row unsigned and runs no reviewed dry run" (toString .Values.signing.mode) $replicas (toString .Values.strategy.type) $mode) }}
{{- end }}
{{- end }}
{{- if and .Release.IsInstall (gt $replicas 0) (not .Values.migration.storeReady) }}
{{- fail (printf "helm install with replicaCount=%d requires migration.storeReady=true, which states that the store is empty or has completed this release's §13.4 stored-row migration (the content-hash-framing completion record in data_migrations); an install over a v0.4.0 store installs at replicaCount=0 with migration.previousImage and follows the upgrade procedure in docs/deployment/clustered.md from step 2" $replicas) }}
{{- end }}
{{- if and .Release.IsInstall (eq $replicas 0) (not .Values.migration.storeReady) (not .Values.migration.previousImage) }}
{{- fail "helm install with replicaCount=0 requires migration.previousImage, the image the store's previous registry ran, or migration.storeReady=true for a store that is empty or has completed this release's §13.4 stored-row migration. The install has no live Deployment to read that image from, and the image gate compares the migrate Job's image against it" }}
{{- end }}
{{- if .Values.migration.preflight }}
{{- if $migrating }}
{{- include "podium.migratedGate" . }}
{{- include "podium.stopGate" . }}
{{- include "podium.imageGate" . }}
{{- end }}
{{- if eq $mode "run" }}
{{- include "podium.reviewedDryRunGate" . }}
{{- end }}
{{- if include "podium.servingPreflight" . }}
{{- include "podium.servingGate" . }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
Migrated-store gate. Once a serving render writes stored-row-format for this
migration, the chart drops pre-migration-image and scales the Deployment up, so
a migration render could pass neither the stop gate nor the image gate. The
gate names that case first, before the stop gate asks for a wait that a serving
Deployment never satisfies. A rerun over a migrated store rewrites nothing and
needs no stop, so it runs on a serving pod through kubectl exec.
*/}}
{{- define "podium.migratedGate" -}}
{{- $live := include "podium.liveDeployment" . | fromJson }}
{{- $format := include "podium.storedRowFormat" . }}
{{- if eq (toString (dig "metadata" "annotations" "podium.lennylabs.dev/stored-row-format" "" $live)) $format }}
{{- fail (printf "the migrated-store gate refuses migration.mode=%s: the live Deployment %s records podium.lennylabs.dev/stored-row-format=%s, so the store completed this migration and the chart renders no migrate Job for it. A rerun rewrites no content hash over a migrated store, and it runs on a serving pod: run `kubectl exec deployment/%s -- /usr/local/bin/podium-server sign-stored-rows%s --dry-run`, review every row with signed_by=unsigned and sign=true, then run the same command without --dry-run" (toString .Values.migration.mode) (include "podium.fullname" .) $format (include "podium.fullname" .) (ternary " --include-unsigned" "" (eq (toString .Values.migration.includeUnsigned) "true"))) }}
{{- end }}
{{- end -}}

{{/*
Stop gate. The §13.4 rewrite requires every previous-version registry process
stopped, and sign-stored-rows binds nothing and cannot detect one, so a
migration render fails while any registry pod of the release is live. A pod in
its termination grace period carries a deletionTimestamp and can still write.
A pod in phase Succeeded or Failed with no deletionTimestamp, such as an
evicted pod, runs no container and persists until pod garbage collection, so
it does not count.
*/}}
{{- define "podium.stopGate" -}}
{{- $name := include "podium.name" . }}
{{- $live := list }}
{{- range (dig "items" (list) (lookup "v1" "Pod" .Release.Namespace "")) }}
{{- $labels := dig "metadata" "labels" (dict) . }}
{{- if and (eq (toString (get $labels "app.kubernetes.io/name")) $name) (eq (toString (get $labels "app.kubernetes.io/instance")) $.Release.Name) }}
{{- $phase := dig "status" "phase" "" . }}
{{- if or (dig "metadata" "deletionTimestamp" "" .) (has $phase (list "Pending" "Running" "Unknown")) }}
{{- $live = append $live (dig "metadata" "name" "" .) }}
{{- end }}
{{- end }}
{{- end }}
{{- $replicas := int (dig "spec" "replicas" 0 (include "podium.liveDeployment" . | fromJson)) }}
{{- if and $live (gt $replicas 0) }}
{{- fail (printf "registry pods of this release are still live or terminating: %s; the §13.4 rewrite requires every registry process stopped, and the live Deployment %s runs %d replica(s). Run the zero-replica step of the upgrade procedure in docs/deployment/clustered.md (helm upgrade with replicaCount=0 and no migration.mode), wait for the pods to be deleted, and rerun this step" (join ", " $live) (include "podium.fullname" .) $replicas) }}
{{- else if $live }}
{{- fail (printf "registry pods of this release are still live or terminating: %s; the §13.4 rewrite requires every registry process stopped. Wait with `kubectl wait --for=delete pod --timeout=10m -l app.kubernetes.io/name=%s,app.kubernetes.io/instance=%s --field-selector=status.phase!=Succeeded,status.phase!=Failed` and rerun this step" (join ", " $live) $name .Release.Name) }}
{{- end }}
{{- end -}}

{{/*
Image gate. v0.4.0's podium-server ignores its arguments, so a migrate Job on
the v0.4.0 image boots a full v0.4.0 registry that never exits. The Job's image
must differ from the one the Deployment ran before the migration.

A live Deployment with no pre-migration-image that already runs the rendered
image is most often a zero-replica step that kept the previous tag, because
podium.migrationAnnotations records nothing when the two images are equal. The
refusal names that cause first, and names the annotate command for the rarer
Deployment moved to this release by a render that recorded nothing.
*/}}
{{- define "podium.imageGate" -}}
{{- $live := include "podium.liveDeployment" . | fromJson }}
{{- $image := include "podium.image" . }}
{{- if not $live }}
{{- fail (printf "the image gate found no live Deployment %s; image.repository and image.tag must name this release, and the zero-replica step of the upgrade procedure in docs/deployment/clustered.md runs first. Rendered image: %s" (include "podium.fullname" .) $image) }}
{{- end }}
{{- $pre := dig "metadata" "annotations" "podium.lennylabs.dev/pre-migration-image" "" $live }}
{{- $liveImage := include "podium.liveImage" . }}
{{- if and (not $pre) (eq $liveImage $image) }}
{{- fail (printf "the image gate refuses the image %s: image.repository and image.tag must name this release. The live Deployment and this render both run %s, and the Deployment carries no podium.lennylabs.dev/pre-migration-image annotation, so the zero-replica step kept the previous image. Set image.repository and image.tag to this release's image and rerun the zero-replica step. When a render that recorded nothing, such as one with migration.preflight=false, has already moved the Deployment to this release's image, record the previous image with `kubectl annotate deployment/%s podium.lennylabs.dev/pre-migration-image=<previous image>` and rerun this step" $image $image (include "podium.fullname" .)) }}
{{- end }}
{{- if not $pre }}
{{- fail (printf "the image gate found no podium.lennylabs.dev/pre-migration-image annotation on the live Deployment, so the zero-replica step kept the previous image; image.repository and image.tag must name this release. Live image: %s; rendered image: %s. Set both and rerun the zero-replica step" $liveImage $image) }}
{{- end }}
{{- if eq $pre $image }}
{{- fail (printf "the image gate refuses the image %s, which the Deployment ran before this migration; image.repository and image.tag must name this release. Previous image: %s; rendered image: %s" $image $pre $image) }}
{{- end }}
{{- end -}}

{{/*
Reviewed-dry-run gate. The run attests every unsigned row the store holds, so it
renders only after a dry run the operator reviewed: the migrate Job named by UID
must be a succeeded dry run of the preceding release revision against the live
Deployment, with the same image, migration name, includeUnsigned setting, and
pod configuration. A run under another store, object-store, or signing-key
configuration would read, and attest, rows the dry run never listed.
*/}}
{{- define "podium.reviewedDryRunGate" -}}
{{- $job := include "podium.liveMigrateJob" . | fromJson }}
{{- $ann := dig "metadata" "annotations" (dict) $job }}
{{- $want := toString .Values.migration.reviewedDryRun }}
{{- $fail := "" }}
{{- if not $job }}
{{- $fail = printf "no migrate Job %s exists" (include "podium.migrateFullname" .) }}
{{- else if ne (toString (dig "metadata" "uid" "" $job)) $want }}
{{- $fail = printf "the migrate Job's uid is %s, not %s" (toString (dig "metadata" "uid" "" $job)) $want }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/deployment-uid")) (include "podium.liveDeploymentUID" .) }}
{{- $fail = printf "the migrate Job's deployment-uid is %q, not the live Deployment's uid %q, so an earlier instance of the release, such as one removed by helm uninstall, ran it" (toString (get $ann "podium.lennylabs.dev/deployment-uid")) (include "podium.liveDeploymentUID" .) }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/migration-mode")) "dry-run" }}
{{- $fail = printf "the migrate Job's migration-mode is %q, not dry-run" (toString (get $ann "podium.lennylabs.dev/migration-mode")) }}
{{- else if lt (int (dig "status" "succeeded" 0 $job)) 1 }}
{{- $fail = "the dry-run Job has not succeeded" }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/stored-row-format")) (include "podium.storedRowFormat" .) }}
{{- $fail = printf "the dry-run Job's stored-row-format is %q, not %s" (toString (get $ann "podium.lennylabs.dev/stored-row-format")) (include "podium.storedRowFormat" .) }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/image")) (include "podium.image" .) }}
{{- $fail = printf "the dry-run Job's image is %s, not %s" (toString (get $ann "podium.lennylabs.dev/image")) (include "podium.image" .) }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/include-unsigned")) (toString .Values.migration.includeUnsigned) }}
{{- $fail = printf "the dry-run Job's include-unsigned is %s, not %s" (toString (get $ann "podium.lennylabs.dev/include-unsigned")) (toString .Values.migration.includeUnsigned) }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/release-revision")) (toString (sub (int .Release.Revision) 1)) }}
{{- $fail = printf "the dry-run Job's release-revision is %s, not %d, so another upgrade or rollback ran after it" (toString (get $ann "podium.lennylabs.dev/release-revision")) (sub (int .Release.Revision) 1) }}
{{- else if ne (toString (get $ann "podium.lennylabs.dev/pod-config")) (include "podium.podConfigDigest" .) }}
{{- $fail = "the dry-run Job's pod-config digest differs from this render's, so the store, object-store, signing-key, or other environment and volume values changed after the dry run" }}
{{- end }}
{{- if $fail }}
{{- fail (printf "the reviewed-dry-run gate refuses migration.mode=run: %s. Rerun the migration.mode=dry-run step, review its log, and pass that Job's UID as migration.reviewedDryRun" $fail) }}
{{- end }}
{{- end -}}

{{/*
Serving preflight. A render that scales the Deployment above zero over a store
that has not completed the migration would start registries that run the
rewrite at boot under the startup probe, beside any previous-version pod the
rolling update keeps. It passes when the live Deployment records the migration,
before it reads any Job. Otherwise it passes only when the migrate Job is a
succeeded run of the preceding release revision against the live Deployment,
for the same migration, image, and pod configuration. The pod-config tie keeps
a run against one store from admitting a serving render whose values point at
another. A Job that recorded another Deployment's UID belongs to an earlier
instance of the release, whose revision numbers a reinstall restarts, so the
gate ignores it.

An upgrade that finds no live Deployment (one deleted by hand, or a render
without cluster access) cannot tell whether the store is migrated, so it
requires migration.storeReady, as an install does.
*/}}
{{- define "podium.servingGate" -}}
{{- $live := include "podium.liveDeployment" . | fromJson }}
{{- $format := include "podium.storedRowFormat" . }}
{{- $liveFormat := toString (dig "metadata" "annotations" "podium.lennylabs.dev/stored-row-format" "" $live) }}
{{- if and .Release.IsUpgrade (not $live) (not .Values.migration.storeReady) }}
{{- fail (printf "the serving preflight refuses replicaCount=%d on a helm upgrade that finds no live Deployment %s: the render cannot tell whether the store has completed this release's §13.4 stored-row migration (the content-hash-framing completion record in data_migrations). Set migration.storeReady=true only for a store that is empty or has completed that migration; otherwise upgrade at replicaCount=0 with migration.previousImage and follow the upgrade procedure in docs/deployment/clustered.md from step 2. A render without cluster access, such as helm template, also finds no Deployment" (int .Values.replicaCount) (include "podium.fullname" .)) }}
{{- end }}
{{- if and $live (ne $liveFormat $format) }}
{{- $job := include "podium.liveMigrateJob" . | fromJson }}
{{- if ne (toString (dig "metadata" "annotations" "podium.lennylabs.dev/deployment-uid" "" $job)) (include "podium.liveDeploymentUID" .) }}
{{- $job = dict }}
{{- end }}
{{- $ann := dig "metadata" "annotations" (dict) $job }}
{{- if and $job (lt (int (dig "status" "succeeded" 0 $job)) 1) }}
{{- fail (printf "the last migration Job did not succeed; read `kubectl logs job/%s`, then rerun the dry-run and run steps of the upgrade procedure in docs/deployment/clustered.md" (include "podium.migrateFullname" .)) }}
{{- end }}
{{- $runOK := and $job (eq (toString (get $ann "podium.lennylabs.dev/migration-mode")) "run") (eq (toString (get $ann "podium.lennylabs.dev/stored-row-format")) $format) (eq (toString (get $ann "podium.lennylabs.dev/image")) (include "podium.image" .)) (eq (toString (get $ann "podium.lennylabs.dev/release-revision")) (toString (sub (int .Release.Revision) 1))) (eq (toString (get $ann "podium.lennylabs.dev/pod-config")) (include "podium.podConfigDigest" .)) }}
{{- if not $runOK }}
{{- fail "the serving preflight refuses replicaCount above zero: the live Deployment predates the content-hash framing, or the last run Job does not belong to the preceding revision of this Deployment with this image and pod configuration (the store, object-store, signing-key, or other environment and volume values); follow the upgrade from v0.4.0 in docs/deployment/clustered.md" }}
{{- end }}
{{- end }}
{{- end -}}

{{/*
The existingSecret reference. The guard drops the block when existingSecret is
empty, because a secretRef with no name is rejected by the API server.
*/}}
{{- define "podium.envFrom" -}}
{{- if .Values.existingSecret }}
envFrom:
  - secretRef:
      name: {{ .Values.existingSecret }}
{{- end }}
{{- end -}}

{{/*
The registry's configuration environment, in order.
*/}}
{{- define "podium.env" -}}
{{- $signingKey := eq .Values.signing.mode "registry-key" }}
env:
  {{- if .Values.postgresql.enabled }}
  # A chart cannot read a secret's value, so the bundled database's
  # password reaches the DSN through the kubelet's $(VAR) expansion,
  # which resolves only variables defined earlier in this same env
  # list. Keep these two in this order, and keep both out of envFrom,
  # which that expansion does not read.
  #
  # The DSN takes lib/pq's keyword form rather than the postgres://
  # URL the driver also accepts, because the expansion substitutes
  # the password literally and a URL would have to carry it
  # percent-encoded. A password holding / ? # % or a space breaks
  # URL parsing, and `openssl rand -base64` emits / routinely, so the
  # URL form fails at connect time on an ordinary generated password
  # and reports it as an invalid port. Quoting the value here keeps a
  # password with a space intact. A single quote or a trailing
  # backslash is rejected when the DSN is parsed, so the pod fails
  # with a connection-string error. An embedded backslash is instead
  # stripped from the value the driver sends, so the database rejects
  # the credential and the pod reports a password-authentication
  # failure that names nothing about escaping. Avoid both.
  #
  # TLS is off because both ends are in-cluster and this database is
  # for evaluation.
  - name: PODIUM_PG_PASSWORD
    valueFrom:
      secretKeyRef:
        name: {{ .Values.postgresql.existingSecret }}
        key: {{ .Values.postgresql.passwordKey }}
  - name: PODIUM_POSTGRES_DSN
    value: "host={{ include "podium.pgFullname" . }} port=5432 user={{ .Values.postgresql.username }} password='$(PODIUM_PG_PASSWORD)' dbname={{ .Values.postgresql.database }} sslmode=disable"
  {{- end }}
  # The kubelet probes the pod over the cluster network, so the
  # registry must listen on all interfaces rather than on its
  # 127.0.0.1 default. values.yaml declared config.bind and no
  # template consumed it, which left every pod failing its probes.
  - name: PODIUM_BIND
    value: {{ .Values.config.bind | quote }}
  - name: PODIUM_REGISTRY_STORE
    value: {{ .Values.config.store.type | quote }}
  - name: PODIUM_OBJECT_STORE
    value: {{ .Values.config.objectStore.type | quote }}
  {{- if eq .Values.config.objectStore.type "s3" }}
  {{- with .Values.config.objectStore.bucket }}
  - name: PODIUM_S3_BUCKET
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.objectStore.region }}
  - name: PODIUM_S3_REGION
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.objectStore.endpoint }}
  - name: PODIUM_S3_ENDPOINT
    value: {{ . | quote }}
  {{- end }}
  {{- if .Values.config.objectStore.s3ForcePathStyle }}
  - name: PODIUM_S3_FORCE_PATH_STYLE
    value: "true"
  {{- end }}
  {{- end }}
  {{- if eq .Values.config.objectStore.type "filesystem" }}
  - name: PODIUM_FILESYSTEM_ROOT
    value: {{ .Values.config.objectStore.filesystemRoot | quote }}
  {{- end }}
  - name: PODIUM_VECTOR_BACKEND
    value: {{ .Values.config.vectorBackend.type | quote }}
  - name: PODIUM_EMBEDDING_PROVIDER
    value: {{ .Values.config.embeddingProvider.type | quote }}
  - name: PODIUM_IDENTITY_PROVIDER
    value: {{ .Values.config.identityProvider.type | quote }}
  {{- with .Values.config.identityProvider.issuer }}
  - name: PODIUM_OAUTH_ISSUER
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.identityProvider.audience }}
  - name: PODIUM_OAUTH_AUDIENCE
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.identityProvider.groupsClaim }}
  - name: PODIUM_OAUTH_GROUPS_CLAIM
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.identityProvider.idpGroupMapping }}
  - name: PODIUM_IDP_GROUP_MAPPING
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.bootstrapAdmins }}
  - name: PODIUM_BOOTSTRAP_ADMINS
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.config.defaultLayerVisibility }}
  - name: PODIUM_DEFAULT_LAYER_VISIBILITY
    value: {{ . | quote }}
  {{- end }}
  {{- if .Values.runtimeKeys.enabled }}
  # The variable names the JSON key file, and the volume mounts the
  # secret at the directory above it. Pointing it at mountPath makes
  # the registry read a directory, which it treats as a fatal boot
  # error rather than as an absent file.
  - name: PODIUM_RUNTIME_KEYS_PATH
    value: {{ printf "%s/%s" (trimSuffix "/" .Values.runtimeKeys.mountPath) .Values.runtimeKeys.key | quote }}
  {{- end }}
  # Every replica signs under the one key the Secret carries (§4.7.9).
  # The variable names the key file inside the mount, as
  # PODIUM_RUNTIME_KEYS_PATH does, because the registry reads it as a
  # file. The loader reads an existing file and writes nothing, so the
  # read-only mount is sufficient.
  - name: PODIUM_SIGN
    value: {{ .Values.signing.mode | quote }}
  {{- if $signingKey }}
  - name: PODIUM_SIGN_KEY_PATH
    value: {{ printf "%s/%s" (trimSuffix "/" .Values.signing.mountPath) .Values.signing.key | quote }}
  {{- end }}
  # Public mode and its bind override travel together. The bind above
  # is 0.0.0.0 so the kubelet can reach the probes, and public mode
  # refuses a non-loopback bind on its own, so a chart that offered
  # only the first of these could not express public mode at all.
  {{- if .Values.config.publicMode }}
  - name: PODIUM_PUBLIC_MODE
    value: "true"
  {{- end }}
  {{- if .Values.config.allowPublicBind }}
  - name: PODIUM_ALLOW_PUBLIC_BIND
    value: "true"
  {{- end }}
  {{- /* The deadline on each object-store read during the §13.4 rewrite, a
  Go duration. The registry's default is 30s. */}}
  {{- with .Values.config.migrationObjectReadTimeout }}
  - name: PODIUM_MIGRATION_OBJECT_READ_TIMEOUT
    value: {{ . | quote }}
  {{- end }}
  {{- /* The audit sink. The root filesystem is read-only, so the default
  file under the home directory cannot be written; an http(s) endpoint keeps
  the artifact.signed events a migration run appends. */}}
  {{- with .Values.config.auditLogPath }}
  - name: PODIUM_AUDIT_LOG_PATH
    value: {{ . | quote }}
  {{- end }}
  {{- with .Values.extraEnv }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end -}}

{{/*
The container mounts. The Job keeps the runtime-keys mount so both pods read
identical configuration.
*/}}
{{- define "podium.volumeMounts" -}}
{{- $signingKey := eq .Values.signing.mode "registry-key" }}
{{- if or .Values.objects.enabled .Values.runtimeKeys.enabled $signingKey }}
volumeMounts:
  {{- if .Values.objects.enabled }}
  - name: objects
    mountPath: {{ .Values.config.objectStore.filesystemRoot }}
  {{- end }}
  {{- if .Values.runtimeKeys.enabled }}
  - name: keys
    mountPath: {{ .Values.runtimeKeys.mountPath }}
    readOnly: true
  {{- end }}
  {{- if $signingKey }}
  - name: signing
    mountPath: {{ .Values.signing.mountPath }}
    readOnly: true
  {{- end }}
{{- end }}
{{- end -}}

{{/*
The pod volumes behind podium.volumeMounts.
*/}}
{{- define "podium.volumes" -}}
{{- $signingKey := eq .Values.signing.mode "registry-key" }}
{{- if or .Values.objects.enabled .Values.runtimeKeys.enabled $signingKey }}
volumes:
  {{- if .Values.objects.enabled }}
  - name: objects
    persistentVolumeClaim:
      claimName: {{ .Values.objects.existingClaim | default (printf "%s-objects" (include "podium.fullname" .)) }}
  {{- end }}
  {{- if .Values.runtimeKeys.enabled }}
  - name: keys
    secret:
      secretName: {{ required "runtimeKeys.secretName is required when runtimeKeys.enabled" .Values.runtimeKeys.secretName }}
  {{- end }}
  {{- if $signingKey }}
  - name: signing
    secret:
      secretName: {{ required "signing.secretName is required unless signing.mode=none" .Values.signing.secretName }}
  {{- end }}
{{- end }}
{{- end -}}

{{/*
Node placement, so the Job lands where the Deployment's pods may land.
*/}}
{{- define "podium.scheduling" -}}
{{- with .Values.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}
