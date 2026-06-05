#!/usr/bin/env bash
#
# install-dev-deps.sh
#
# Installs the local development dependencies needed to run Podium's live
# backend tests (real Postgres + pgvector, real S3 via MinIO). The §4.7.1
# Postgres isolation work (schema-per-org + row-level security) needs a real
# Postgres to verify, which this provisions.
#
# Two paths are supported:
#   - Docker (default): pull the pgvector/pgvector:pg16 and MinIO images that
#     docker-compose.yml uses, then start them via `make services-up`.
#   - Native (--native): install Postgres 16 + pgvector and start it locally.
#
# Usage:
#   scripts/install-dev-deps.sh            # Docker path (recommended)
#   scripts/install-dev-deps.sh --native   # native Postgres path (macOS/Homebrew or apt)
#
set -euo pipefail

MODE="docker"
[ "${1:-}" = "--native" ] && MODE="native"

uname_s="$(uname -s)"

have() { command -v "$1" >/dev/null 2>&1; }

install_docker_path() {
  if ! have docker; then
    echo "Docker is required for the default path but is not installed."
    case "$uname_s" in
      Darwin)
        if have brew; then
          echo "Installing Docker Desktop via Homebrew..."
          brew install --cask docker
          echo "Launch Docker Desktop once to start the daemon, then re-run this script."
          exit 0
        fi
        echo "Install Docker Desktop: https://www.docker.com/products/docker-desktop/"
        exit 1
        ;;
      Linux)
        echo "Install Docker Engine: https://docs.docker.com/engine/install/"
        echo "  (Debian/Ubuntu: 'sudo apt-get install docker.io', then add your user to the docker group.)"
        exit 1
        ;;
      *)
        echo "Unsupported OS '$uname_s' for automatic Docker install; install Docker manually."
        exit 1
        ;;
    esac
  fi

  if ! docker info >/dev/null 2>&1; then
    echo "Docker is installed but the daemon is not running. Start Docker, then re-run."
    exit 1
  fi

  echo "Pulling the images docker-compose.yml uses..."
  docker pull pgvector/pgvector:pg16
  docker pull minio/minio:RELEASE.2024-10-29T15-34-59Z || docker pull minio/minio:latest

  echo "Starting Postgres + MinIO + bucket bootstrap..."
  make services-up
  echo
  echo "Done. Postgres (:5432) and MinIO (:9000, console :9001) are up; the"
  echo "bootstrap container created the 'podium' bucket. Verify with:"
  echo "    scripts/preflight-postgres.sh"
  echo "    scripts/preflight-minio.sh"
}

install_native_path() {
  case "$uname_s" in
    Darwin)
      have brew || { echo "Homebrew required for the native path on macOS. https://brew.sh"; exit 1; }
      echo "Installing Postgres 16 + pgvector via Homebrew..."
      brew install postgresql@16 pgvector
      brew services start postgresql@16
      echo "Creating the podium role and database..."
      createuser -s podium 2>/dev/null || true
      psql postgres -c "ALTER ROLE podium WITH PASSWORD 'podium';" 2>/dev/null || true
      createdb -O podium podium 2>/dev/null || true
      psql "postgres://podium:podium@localhost:5432/podium?sslmode=disable" \
        -c "CREATE EXTENSION IF NOT EXISTS vector;"
      ;;
    Linux)
      have apt-get || { echo "Native path currently scripts apt-based distros only; install Postgres 16 + pgvector manually."; exit 1; }
      echo "Installing Postgres 16 + pgvector via apt..."
      sudo apt-get update
      sudo apt-get install -y postgresql-16 postgresql-16-pgvector
      sudo -u postgres psql -c "CREATE ROLE podium LOGIN SUPERUSER PASSWORD 'podium';" 2>/dev/null || true
      sudo -u postgres createdb -O podium podium 2>/dev/null || true
      sudo -u postgres psql -d podium -c "CREATE EXTENSION IF NOT EXISTS vector;"
      ;;
    *)
      echo "Unsupported OS '$uname_s' for the native path."
      exit 1
      ;;
  esac
  install_native_minio
  echo
  echo "Done. Verify with:"
  echo "    scripts/preflight-postgres.sh"
  echo "    scripts/preflight-minio.sh"
}

# install_native_minio installs the MinIO server + mc client, starts a local
# server with the docker-compose credentials (minioadmin/minioadmin) and data
# under .podium-dev/minio-data/, then creates the `podium` bucket. This gives
# the object-store live tests (PODIUM_S3_*) a real backend on the native path,
# matching what the Docker path provides via `make services-up`.
install_native_minio() {
  local data_dir alias_set
  data_dir="$(pwd)/.podium-dev/minio-data"
  mkdir -p "$data_dir"

  if ! have minio; then
    case "$uname_s" in
      Darwin)
        have brew || { echo "Homebrew required to install MinIO on macOS."; exit 1; }
        echo "Installing MinIO server + client via Homebrew..."
        brew install minio/stable/minio minio/stable/mc
        ;;
      Linux)
        echo "Installing MinIO server + client binaries from dl.min.io..."
        local bindir="${HOME}/.local/bin"; mkdir -p "$bindir"
        curl -fsSL https://dl.min.io/server/minio/release/linux-amd64/minio  -o "$bindir/minio" && chmod +x "$bindir/minio"
        curl -fsSL https://dl.min.io/client/mc/release/linux-amd64/mc        -o "$bindir/mc"    && chmod +x "$bindir/mc"
        case ":$PATH:" in *":$bindir:"*) ;; *) echo "Add $bindir to your PATH.";; esac
        export PATH="$bindir:$PATH"
        ;;
    esac
  else
    echo "MinIO already installed."
  fi

  if nc -z -w2 localhost 9000 >/dev/null 2>&1 || (exec 3<>/dev/tcp/localhost/9000) 2>/dev/null; then
    echo "MinIO already serving on :9000."
  else
    echo "Starting MinIO server in the background (console on :9001)..."
    MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin \
      nohup minio server "$data_dir" --address ':9000' --console-address ':9001' \
      >.podium-dev/minio.log 2>&1 &
    # Wait for readiness (bounded).
    for _ in $(seq 1 20); do
      nc -z -w1 localhost 9000 >/dev/null 2>&1 && break
      sleep 0.5
    done
  fi

  # Create the bucket with mc (idempotent).
  if have mc; then
    mc alias set podiumlocal http://localhost:9000 minioadmin minioadmin >/dev/null 2>&1 && alias_set=1
    [ -n "${alias_set:-}" ] && mc mb --ignore-existing podiumlocal/podium >/dev/null 2>&1 && echo "Bucket 'podium' ready."
  else
    echo "mc client not found; create the 'podium' bucket manually via the console at http://localhost:9001"
  fi
}

echo "Podium dev-dependency install (mode: $MODE)"
case "$MODE" in
  docker) install_docker_path ;;   # Docker path provisions MinIO via `make services-up` (minio + bucket bootstrap)
  native) install_native_path ;;   # native path installs Postgres + pgvector and MinIO + mc
esac
