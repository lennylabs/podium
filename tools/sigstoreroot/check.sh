#!/usr/bin/env bash
# Checks that the embedded public-good Sigstore trusted root is the current
# target of the Sigstore TUF repository (§4.7.11). A fetch that fails is
# retried twice, after 30 and 60 seconds, so one unavailable request does not
# fail a release; a root that differs fails at once. Run from the module root.
set -euo pipefail

bin="$(mktemp -d)/sigstoreroot"
# go run reports every non-zero exit as 1, so the tool is built and run
# directly to keep its exit status: 3 means the fetch failed.
go build -o "${bin}" ./tools/sigstoreroot

for attempt in 1 2 3; do
  status=0
  "${bin}" -check || status=$?
  if [ "${status}" -ne 3 ]; then
    exit "${status}"
  fi
  if [ "${attempt}" -lt 3 ]; then
    echo "::warning::fetching the Sigstore TUF repository failed (attempt ${attempt} of 3); retrying" >&2
    sleep $((30 * attempt))
  fi
done
echo "::error::the Sigstore TUF repository could not be reached in 3 attempts" >&2
exit 3
