#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   ./download-latest-bundle.sh
# Optional env overrides:
#   KUBECONFIG=/etc/rancher/k3s/k3s.yaml
#   OPS_NS=forensics-system
#   OUT_DIR=./downloads
#   CONTROLLER_LABEL=app=forensic-controller

KUBECONFIG="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
OPS_NS="${OPS_NS:-forensics-system}"
OUT_DIR="${OUT_DIR:-./downloads}"
CONTROLLER_LABEL="${CONTROLLER_LABEL:-app=forensic-controller}"
CRD="forensicsnapshots.forensics.cybernet.dev"

export KUBECONFIG

echo "[1/7] Checking cluster and CRD..."
kubectl cluster-info >/dev/null
kubectl get crd "$CRD" >/dev/null

echo "[2/7] Finding latest snapshot..."
LATEST_LINE="$(kubectl get "$CRD" -A --no-headers --sort-by=.metadata.creationTimestamp | tail -n 1 || true)"
if [ -z "$LATEST_LINE" ]; then
  echo "No ForensicSnapshot resources found."
  exit 1
fi

SNAP_NS="$(echo "$LATEST_LINE" | awk '{print $1}')"
SNAP_NAME="$(echo "$LATEST_LINE" | awk '{print $2}')"

PHASE="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.phase}')"
BUNDLE_PATH="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.bundlePath}')"
EXPECTED_SHA="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.sha256}')"

echo "Latest snapshot: $SNAP_NS/$SNAP_NAME"
echo "Phase: $PHASE"
echo "BundlePath: $BUNDLE_PATH"
echo "ExpectedSHA: $EXPECTED_SHA"

echo "[3/7] Locating controller pod..."
CTRL_POD="$(kubectl get pods -n "$OPS_NS" -l "$CONTROLLER_LABEL" -o jsonpath='{.items[0].metadata.name}')"
if [ -z "$CTRL_POD" ]; then
  echo "No controller pod found in namespace '$OPS_NS' with label '$CONTROLLER_LABEL'."
  exit 1
fi

echo "Controller pod: $CTRL_POD"

echo "[4/7] Verifying bundle exists in controller pod..."
if [ -z "$BUNDLE_PATH" ]; then
  echo "Snapshot status.bundlePath is empty; trying auto-discovery by snapshot name..."
  BUNDLE_PATH="$(kubectl exec -n "$OPS_NS" "$CTRL_POD" -- sh -c "find /var/lib/forensics -type f -name '*${SNAP_NAME}*' | head -n 1")"
  if [ -z "$BUNDLE_PATH" ]; then
    echo "Could not discover bundle path in /var/lib/forensics."
    exit 1
  fi
  echo "Discovered BundlePath: $BUNDLE_PATH"
fi

kubectl exec -n "$OPS_NS" "$CTRL_POD" -- sh -c "ls -lh '$BUNDLE_PATH'"

echo "[5/7] Downloading bundle..."
mkdir -p "$OUT_DIR"
LOCAL_FILE="$OUT_DIR/${SNAP_NAME}.bundle.tar.gz"
kubectl cp "$OPS_NS/$CTRL_POD:$BUNDLE_PATH" "$LOCAL_FILE"
ls -lh "$LOCAL_FILE"

echo "[6/7] Verifying checksum..."
LOCAL_SHA="$(sha256sum "$LOCAL_FILE" | awk '{print $1}')"
echo "LocalSHA:    $LOCAL_SHA"
if [ -n "$EXPECTED_SHA" ]; then
  echo "ExpectedSHA: $EXPECTED_SHA"
  if [ "$LOCAL_SHA" = "$EXPECTED_SHA" ]; then
    echo "Checksum: OK"
  else
    echo "Checksum: MISMATCH"
    exit 2
  fi
else
  echo "ExpectedSHA empty in status; skipped strict compare."
fi

echo "[7/7] Inspecting archive..."
if tar -tzf "$LOCAL_FILE" >/dev/null 2>&1; then
  tar -tzf "$LOCAL_FILE" | head -n 50
else
  echo "Archive is not a valid tar.gz (or is empty placeholder)."
fi

echo "Done: $LOCAL_FILE"
