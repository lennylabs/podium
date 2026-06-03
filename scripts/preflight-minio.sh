#!/usr/bin/env bash
#
# preflight-minio.sh
#
# Reports whether a MinIO (or S3-compatible) object store usable by the live
# object-store tests (PODIUM_S3_*) is reachable, and prints the PODIUM_S3_*
# env to export. Exits 0 when a usable endpoint + bucket is found, non-zero
# with remediation guidance otherwise.
#
# The §6.6 presigned-body / bundled-resource delivery work needs a real object
# store to verify; run this first, then `make test-live` (or set the env and
# run `go test ./pkg/objectstore/... ./test/...`).
#
# Usage:
#   scripts/preflight-minio.sh             # check; print the env on success
#   eval "$(scripts/preflight-minio.sh --export)"   # set PODIUM_S3_* in the shell
#
set -euo pipefail

# The endpoint is a URL whose scheme selects TLS (ParseS3Endpoint, §13.12);
# http:// is required for a plaintext local MinIO.
ENDPOINT="${PODIUM_S3_ENDPOINT:-http://localhost:9000}"
BUCKET="${PODIUM_S3_BUCKET:-podium}"
KEY="${PODIUM_S3_ACCESS_KEY_ID:-minioadmin}"
SECRET="${PODIUM_S3_SECRET_ACCESS_KEY:-minioadmin}"

EXPORT_MODE="no"
[ "${1:-}" = "--export" ] && EXPORT_MODE="yes"
log() { [ "$EXPORT_MODE" = "yes" ] || echo "$@" >&2; }

# Split an optional scheme off the endpoint; the scheme selects TLS.
scheme="http"
bare="$ENDPOINT"
case "$ENDPOINT" in
  https://*) scheme="https"; bare="${ENDPOINT#https://}" ;;
  http://*)  scheme="http";  bare="${ENDPOINT#http://}"  ;;
esac
host="${bare%%:*}"
port="${bare##*:}"
[ "$host" = "$port" ] && port="9000"
[ -z "$host" ] && host="localhost"

reachable() {
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 2 "$host" "$port" >/dev/null 2>&1 && return 0
    return 1
  fi
  (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1 && return 0
  return 1
}

if reachable; then
  log "preflight: object store reachable at $ENDPOINT"

  # Best-effort liveness + bucket check via mc when available.
  if command -v mc >/dev/null 2>&1; then
    if mc alias set podiumpf "$scheme://$host:$port" "$KEY" "$SECRET" >/dev/null 2>&1; then
      if mc ls "podiumpf/$BUCKET" >/dev/null 2>&1; then
        log "preflight: bucket '$BUCKET' exists."
      else
        log "preflight: WARNING — bucket '$BUCKET' is missing. Create it:"
        log "           mc mb podiumpf/$BUCKET"
        log "           (the docker-compose bootstrap container creates it; on the native"
        log "            path scripts/install-dev-deps.sh --native does too.)"
      fi
    fi
  fi

  if [ "$EXPORT_MODE" = "yes" ]; then
    echo "export PODIUM_S3_ENDPOINT='$ENDPOINT'"
    echo "export PODIUM_S3_BUCKET='$BUCKET'"
    echo "export PODIUM_S3_ACCESS_KEY_ID='$KEY'"
    echo "export PODIUM_S3_SECRET_ACCESS_KEY='$SECRET'"
  else
    log ""
    log "Ready. The PODIUM_S3_* env above points the live object-store tests at this endpoint."
    log "Run them with:  make test-live   (or set the env and: go test ./pkg/objectstore/...)"
    echo "$ENDPOINT"
  fi
  exit 0
fi

log "preflight: no object store reachable at $ENDPOINT"
log ""
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  log "Docker is running. Start the local services (Postgres + MinIO + bucket):"
  log "    make services-up"
else
  log "Install and start MinIO with:"
  log "    scripts/install-dev-deps.sh            # Docker path (recommended)"
  log "    scripts/install-dev-deps.sh --native   # native MinIO + mc"
fi
log ""
log "Once MinIO is up, re-run this preflight to confirm."
exit 1
