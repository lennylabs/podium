#!/usr/bin/env bash
#
# preflight-postgres.sh
#
# Reports whether a Postgres backend usable by the live store test suite
# (pkg/store/postgres_test.go and the §4.7.1 isolation tests) is reachable,
# and prints the PODIUM_POSTGRES_DSN to export. Exits 0 when a usable
# database is found, non-zero with remediation guidance otherwise.
#
# The §4.7.1 schema-per-org + row-level-security work cannot be verified
# without a real Postgres; run this first, then `make test-live` (or set the
# DSN and run `go test ./pkg/store/...`).
#
# Usage:
#   scripts/preflight-postgres.sh            # check; print the DSN on success
#   eval "$(scripts/preflight-postgres.sh --export)"   # set PODIUM_POSTGRES_DSN in the shell
#
set -euo pipefail

DEFAULT_DSN="postgres://podium:podium@localhost:5432/podium?sslmode=disable"
DSN="${PODIUM_POSTGRES_DSN:-$DEFAULT_DSN}"
EXPORT_MODE="no"
[ "${1:-}" = "--export" ] && EXPORT_MODE="yes"

log()  { [ "$EXPORT_MODE" = "yes" ] || echo "$@" >&2; }
fail() { log "preflight: $*"; exit 1; }

# Prefer a real client probe (psql) when available; fall back to a TCP check.
probe_with_psql() {
  command -v psql >/dev/null 2>&1 || return 2
  if psql "$DSN" -tAc 'select 1' >/dev/null 2>&1; then
    return 0
  fi
  return 1
}

probe_with_tcp() {
  # Parse host:port out of the DSN; default to localhost:5432.
  local hostport host port
  hostport="$(printf '%s' "$DSN" | sed -E 's#^[a-z]+://([^@]*@)?([^/?]+).*#\2#')"
  host="${hostport%%:*}"
  port="${hostport##*:}"
  [ "$host" = "$port" ] && port="5432"
  [ -z "$host" ] && host="localhost"
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 2 "$host" "$port" >/dev/null 2>&1 && return 0
    return 1
  fi
  # Bash /dev/tcp fallback when nc is absent.
  (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1 && return 0
  return 1
}

# 1. Is the database reachable?
reachable="no"
if probe_with_psql; then
  reachable="yes"
  method="psql"
elif [ "$?" -ne 2 ] && command -v psql >/dev/null 2>&1; then
  reachable="no"
elif probe_with_tcp; then
  reachable="yes"
  method="tcp"
fi

if [ "$reachable" = "yes" ]; then
  log "preflight: Postgres reachable via ${method:-tcp} at $DSN"
  # Best-effort pgvector check (the embeddings tables need the extension).
  if command -v psql >/dev/null 2>&1; then
    if ! psql "$DSN" -tAc "select 1 from pg_extension where extname='vector'" 2>/dev/null | grep -q 1; then
      log "preflight: WARNING — the 'vector' (pgvector) extension is not installed in this database."
      log "           Install it: psql \"$DSN\" -c 'CREATE EXTENSION IF NOT EXISTS vector;'"
      log "           (the pgvector/pgvector:pg16 image ships it; native Postgres needs the extension package.)"
    fi
  fi
  if [ "$EXPORT_MODE" = "yes" ]; then
    echo "export PODIUM_POSTGRES_DSN='$DSN'"
  else
    echo "$DSN"
    log ""
    log "Ready. Run the live store suite with:"
    log "  PODIUM_POSTGRES_DSN='$DSN' go test ./pkg/store/... -count=1"
    log "or the full live suite with:  make test-live"
  fi
  exit 0
fi

# 2. Not reachable — guide the operator.
log "preflight: no Postgres reachable at $DSN"
log ""
if command -v docker >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    log "Docker is installed and running. Start the local services with:"
    log "    make services-up        # brings up pgvector/pgvector:pg16 on :5432"
    log "    make services-status    # wait until postgres is healthy"
  else
    log "Docker is installed but the daemon is not running. Start Docker Desktop, then:"
    log "    make services-up"
  fi
else
  log "Docker is not installed. Install dev dependencies first:"
  log "    scripts/install-dev-deps.sh"
fi
log ""
log "Once Postgres is up, re-run this preflight to confirm."
exit 1
