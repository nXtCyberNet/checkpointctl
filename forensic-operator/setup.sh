#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="default"
TEST_APP_NAME="test-app"
TEST_IMAGE="nginx:1.25"
SNAPSHOT_TIMEOUT_SECONDS="180"
POLL_INTERVAL_SECONDS="2"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is required but not found in PATH"
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

echo "[1/9] Applying CRD and RBAC/controller manifests"
kubectl apply -f config/crd/bases
kubectl apply -k config/controller
kubectl apply -k config/collector

echo "[2/9] Waiting for forensic-controller deployment"
kubectl -n "${NAMESPACE}" rollout status deploy/forensic-controller --timeout=180s

echo "[3/9] Waiting for checkpoint-collector daemonset"
kubectl -n "${NAMESPACE}" rollout status ds/checkpoint-collector --timeout=180s

echo "[4/9] Creating/refreshing test app"
kubectl -n "${NAMESPACE}" delete deploy "${TEST_APP_NAME}" --ignore-not-found
kubectl -n "${NAMESPACE}" run "${TEST_APP_NAME}" --image="${TEST_IMAGE}" --restart=Always
kubectl -n "${NAMESPACE}" rollout status deploy/"${TEST_APP_NAME}" --timeout=180s

echo "[5/9] Annotating pod to trigger snapshot"
POD_NAME="$(kubectl -n "${NAMESPACE}" get pod -l run=${TEST_APP_NAME} -o jsonpath='{.items[0].metadata.name}')"
kubectl -n "${NAMESPACE}" annotate pod "${POD_NAME}" \
  forensics.cybernet.dev/capture=true \
  forensics.cybernet.dev/containers="${TEST_APP_NAME}" --overwrite

echo "[6/9] Waiting for snapshot CR"
DEADLINE=$(( $(date +%s) + SNAPSHOT_TIMEOUT_SECONDS ))
SNAPSHOT_NAME=""
while [[ $(date +%s) -lt ${DEADLINE} ]]; do
  SNAPSHOT_NAME="$(kubectl -n "${NAMESPACE}" get forensicsnapshots.forensics.cybernet.dev -o jsonpath='{.items[-1:].metadata.name}' 2>/dev/null || true)"
  if [[ -n "${SNAPSHOT_NAME}" ]]; then
    break
  fi
  sleep "${POLL_INTERVAL_SECONDS}"
done

if [[ -z "${SNAPSHOT_NAME}" ]]; then
  echo "Timed out waiting for ForensicSnapshot CR"
  exit 1
fi

echo "Snapshot detected: ${SNAPSHOT_NAME}"

echo "[7/9] Waiting for terminal snapshot phase"
PHASE=""
while [[ $(date +%s) -lt ${DEADLINE} ]]; do
  PHASE="$(kubectl -n "${NAMESPACE}" get forensicsnapshot "${SNAPSHOT_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  if [[ "${PHASE}" == "Sealed" || "${PHASE}" == "Failed" ]]; then
    break
  fi
  sleep "${POLL_INTERVAL_SECONDS}"
done

echo "Snapshot phase: ${PHASE:-unknown}"
if [[ "${PHASE}" != "Sealed" ]]; then
  echo "Snapshot did not seal successfully"
  kubectl -n "${NAMESPACE}" get forensicsnapshot "${SNAPSHOT_NAME}" -o yaml || true
  exit 1
fi

echo "[8/9] Printing snapshot status"
kubectl -n "${NAMESPACE}" get forensicsnapshot "${SNAPSHOT_NAME}" -o jsonpath='Name: {.metadata.name}{"\n"}Phase: {.status.phase}{"\n"}Bundle: {.status.bundlePath}{"\n"}SHA: {.status.bundleSHA256}{"\n"}'
echo

echo "[9/9] Optional bundle download and verify"
if [[ -x "${ROOT_DIR}/download-latest-bundle.sh" ]]; then
  "${ROOT_DIR}/download-latest-bundle.sh" || true
else
  echo "download-latest-bundle.sh not executable or missing; skipping"
fi

echo

echo "Setup completed successfully."
echo "Debug helper: ./diagnose-latest-snapshot.sh"
