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

Every guard reads values only, so helm template and a GitOps controller refuse
what helm upgrade refuses, and rendering needs no cluster permission. The
guards stop at the first fail, so their order sets which refusal a render with
several problems reports.

No guard stands between a serving render and an unmigrated store. In either
signing mode, a registry pod over a store that holds manifest rows and no §13.4
completion record exits at start, and it rewrites no manifest row, signs no
row, and records no completion. Its log names sign-stored-rows.

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
{{- if and $migrating (eq .Values.signing.mode "none") .Values.migration.includeUnsigned }}
{{- fail (printf "migration.mode=%s with signing.mode=none requires migration.includeUnsigned=false: sign-stored-rows refuses --include-unsigned with signing off (config.signature_provider_unavailable), because it signs no row" $mode) }}
{{- end }}
{{- if and $migrating (not .Values.migration.previousImage) }}
{{- fail (printf "migration.mode=%s requires migration.previousImage, the image the store's previous registry ran, such as ghcr.io/lennylabs/podium-server:v0.4.0: the v0.4.0 podium-server ignores its arguments, so a migrate Job on that image starts a v0.4.0 registry that never exits" $mode) }}
{{- end }}
{{- if and $migrating (eq (toString .Values.migration.previousImage) (include "podium.image" .)) }}
{{- fail (printf "migration.mode=%s renders the migrate Job on %s, the image migration.previousImage names as the one the store's previous registry ran; set image.repository and image.tag to this release's image" $mode (include "podium.image" .)) }}
{{- end }}
{{- if and (eq $mode "run") (not (regexMatch "^sha256:[0-9a-f]{64}$" (toString .Values.migration.planDigest))) }}
{{- fail "migration.mode=run requires migration.planDigest, the `sha256:` value on the `dry-run: plan digest` line that ends the reviewed dry run's log; run the migration.mode=dry-run step first and review its log" }}
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
