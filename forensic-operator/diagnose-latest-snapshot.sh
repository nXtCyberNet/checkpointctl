#!/usr/bin/env bash
set -euo pipefail

# Diagnose the latest ForensicSnapshot failure/success path.
# Uses current kubectl context (no KUBECONFIG export here).
#
# Optional env overrides:
#   CRD=forensicsnapshots.forensics.cybernet.dev
#   OPS_NS=forensics-system
#   LOOKBACK_MIN=30

CRD="${CRD:-forensicsnapshots.forensics.cybernet.dev}"
OPS_NS="${OPS_NS:-forensics-system}"
LOOKBACK_MIN="${LOOKBACK_MIN:-30}"

WORKDIR="/tmp/forensic-diag"
mkdir -p "$WORKDIR"

echo "[1/9] Validating kubectl context and CRD..."
kubectl cluster-info >/dev/null
kubectl get crd "$CRD" >/dev/null

echo "[2/9] Finding latest snapshot..."
LATEST_LINE="$(kubectl get "$CRD" -A --no-headers --sort-by=.metadata.creationTimestamp | tail -n 1 || true)"
if [ -z "$LATEST_LINE" ]; then
  echo "No ForensicSnapshot resources found."
  exit 1
fi

SNAP_NS="$(echo "$LATEST_LINE" | awk '{print $1}')"
SNAP_NAME="$(echo "$LATEST_LINE" | awk '{print $2}')"

echo "SNAP_NS=$SNAP_NS SNAP_NAME=$SNAP_NAME"

echo "[3/9] Capturing snapshot status..."
kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o yaml > "$WORKDIR/${SNAP_NAME}.yaml"
kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.phase}{"\n"}{.status.failureReason}{"\n"}{.status.bundlePath}{"\n"}{.status.sha256}{"\n"}'

echo "[4/9] Collecting controller + collector logs..."
kubectl -n "$OPS_NS" logs deploy/forensic-controller --tail=400 > "$WORKDIR/${SNAP_NAME}.controller.log" || true
kubectl -n "$OPS_NS" logs -l app=checkpoint-collector --tail=400 > "$WORKDIR/${SNAP_NAME}.collector.log" || true

echo "---- Controller highlights ----"
grep -Ei "checkpoint|criu|runc|collector|bundle|error|failed|unavailable" "$WORKDIR/${SNAP_NAME}.controller.log" | tail -n 120 || true

echo "---- Collector highlights ----"
grep -Ei "checkpoint|copy|rename|stat|sha|error|failed" "$WORKDIR/${SNAP_NAME}.collector.log" | tail -n 120 || true

echo "[5/9] Node/runtime versions..."
kubectl get nodes -o wide || true
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"  kubelet="}{.status.nodeInfo.kubeletVersion}{"  runtime="}{.status.nodeInfo.containerRuntimeVersion}{"  kernel="}{.status.nodeInfo.kernelVersion}{"\n"}{end}' || true

echo "[6/9] Verifying controller-side bundle path..."
CTRL_POD="$(kubectl get pods -n "$OPS_NS" -l app=forensic-controller -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
BUNDLE_PATH="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.bundlePath}' 2>/dev/null || true)"

echo "CTRL_POD=$CTRL_POD"
echo "BUNDLE_PATH=$BUNDLE_PATH"

if [ -n "$CTRL_POD" ] && [ -n "$BUNDLE_PATH" ]; then
  kubectl exec -n "$OPS_NS" "$CTRL_POD" -- sh -c "ls -lh \"$BUNDLE_PATH\" || true; stat \"$BUNDLE_PATH\" || true" || true
else
  echo "Skipping bundle path verification (missing controller pod or bundle path)."
fi

echo "[7/9] Host-side runtime logs (requires sudo)..."
if command -v sudo >/dev/null 2>&1; then
  sudo journalctl -u k3s -S "-${LOOKBACK_MIN} min" | grep -Ei "checkpoint|criu|runc|containerd|error|fail" | tail -n 300 > "$WORKDIR/${SNAP_NAME}.host-runtime.log" || true
  sudo find /var/lib/rancher/k3s -type f | grep -Ei "criu.*log|dump.log|restore.log" | tail -n 50 > "$WORKDIR/${SNAP_NAME}.criu-log-paths.txt" || true
else
  echo "sudo not found; skipping host-side runtime log collection." | tee "$WORKDIR/${SNAP_NAME}.host-runtime.log"
fi

echo "[8/9] Host binary/runtime versions..."
{
  echo "which tar: $(command -v tar || true)"
  tar --version 2>/dev/null | head -n 1 || true
  runc --version 2>/dev/null || true
  criu --version 2>/dev/null || true
} > "$WORKDIR/${SNAP_NAME}.versions.txt"

if command -v sudo >/dev/null 2>&1; then
  {
    sudo ls -l /var/lib/rancher/k3s/data/current/bin/tar || true
    sudo /var/lib/rancher/k3s/data/current/bin/tar --help 2>/dev/null | head -n 2 || true
  } >> "$WORKDIR/${SNAP_NAME}.versions.txt"
fi

echo "[9/9] Packing diagnostics..."
BUNDLE_OUT="/tmp/forensic-diag-${SNAP_NAME}.tgz"
tar -czf "$BUNDLE_OUT" -C "$WORKDIR" \
  "${SNAP_NAME}.yaml" \
  "${SNAP_NAME}.controller.log" \
  "${SNAP_NAME}.collector.log" \
  "${SNAP_NAME}.host-runtime.log" \
  "${SNAP_NAME}.criu-log-paths.txt" \
  "${SNAP_NAME}.versions.txt" \
  2>/dev/null || true

echo "Diagnostic bundle: $BUNDLE_OUT"
echo "Done."
