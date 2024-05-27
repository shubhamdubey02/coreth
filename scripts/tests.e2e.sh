#!/usr/bin/env bash

set -euo pipefail

# Run CryftGo e2e tests from the target version against the current state of coreth.

# e.g.,
# ./scripts/tests.e2e.sh
# AVALANCHE_VERSION=v1.10.x ./scripts/tests.e2e.sh
if ! [[ "$0" =~ scripts/tests.e2e.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

# Coreth root directory
CORETH_PATH=$(
  cd "$(dirname "${BASH_SOURCE[0]}")"
  cd .. && pwd
)

# Allow configuring the clone path to point to an existing clone
CRYFTGO_CLONE_PATH="${CRYFTGO_CLONE_PATH:-cryftgo}"

# Load the version
source "$CORETH_PATH"/scripts/versions.sh

# Always return to the coreth path on exit
function cleanup {
  cd "${CORETH_PATH}"
}
trap cleanup EXIT

echo "checking out target CryftGo version ${AVALANCHE_VERSION}"
if [[ -d "${CRYFTGO_CLONE_PATH}" ]]; then
  echo "updating existing clone"
  cd "${CRYFTGO_CLONE_PATH}"
  git fetch
else
  echo "creating new clone"
  git clone https://github.com/cryft-labs/cryftgo.git "${CRYFTGO_CLONE_PATH}"
  cd "${CRYFTGO_CLONE_PATH}"
fi
# Branch will be reset to $AVALANCHE_VERSION if it already exists
git checkout -B "test-${AVALANCHE_VERSION}" "${AVALANCHE_VERSION}"

echo "updating coreth dependency to point to ${CORETH_PATH}"
go mod edit -replace "github.com/cryft-labs/coreth=${CORETH_PATH}"
go mod tidy

echo "building cryftgo"
./scripts/build.sh -r

echo "running CryftGo e2e tests"
E2E_SERIAL=1 ./scripts/tests.e2e.sh --ginkgo.label-filter='c || uses-c'
