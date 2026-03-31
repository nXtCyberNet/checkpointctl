#!/usr/bin/env bash
set -euo pipefail

# Full reset + rebuild + redeploy + snapshot debug run for forensic-operator on k3s/containerd.
# Uses current kubectl context.
#tools using - k3s + nerdctl must be installed on host, and user must have permissions to manage k3s and containerd (e.g. via sudo).
# Optional overrides:
#   OPS_NS=forensics-system APP_NS=default TEST_POD=test-app TEST_CONTAINER=test-app
#   K3S_CTR_SOCK=/run/k3s/containerd/containerd.sock CTR_NS=k8s.io
#   CONTROLLER_IMAGE=forensic-controller:latest COLLECTOR_IMAGE=forensic-collector:latest
#   WAIT_SNAPSHOT_SECS=180

OPS_NS="${OPS_NS:-forensics-system}"
APP_NS="${APP_NS:-default}"
TEST_POD="${TEST_POD:-test-app}"
TEST_CONTAINER="${TEST_CONTAINER:-test-app}"
K3S_CTR_SOCK="${K3S_CTR_SOCK:-/run/k3s/containerd/containerd.sock}"
CTR_NS="${CTR_NS:-k8s.io}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-forensic-controller:latest}"
COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-forensic-collector:latest}"
WAIT_SNAPSHOT_SECS="${WAIT_SNAPSHOT_SECS:-180}"
CRD="forensicsnapshots.forensics.cybernet.dev"

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="$ROOT_DIR/debug-run-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LOG_DIR"

echo "[0/14] Preflight"
kubectl cluster-info > "$LOG_DIR/cluster-info.txt"
kubectl get crd "$CRD" -o yaml > "$LOG_DIR/crd.yaml"

cat <<EOF
Run settings:
  ROOT_DIR=$ROOT_DIR
  LOG_DIR=$LOG_DIR
  OPS_NS=$OPS_NS
  APP_NS=$APP_NS
  TEST_POD=$TEST_POD
  TEST_CONTAINER=$TEST_CONTAINER
  CONTROLLER_IMAGE=$CONTROLLER_IMAGE
  COLLECTOR_IMAGE=$COLLECTOR_IMAGE
EOF

echo "[1/14] Delete namespace: $OPS_NS"
kubectl delete namespace "$OPS_NS" --ignore-not-found=true
for i in $(seq 1 120); do
  if ! kubectl get namespace "$OPS_NS" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
if kubectl get namespace "$OPS_NS" >/dev/null 2>&1; then
  echo "Namespace $OPS_NS still exists after wait." >&2
  exit 1
fi

echo "[2/14] Remove old images in containerd namespace"
sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" rmi "$CONTROLLER_IMAGE" --force >/dev/null 2>&1 || true
sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" rmi "$COLLECTOR_IMAGE" --force >/dev/null 2>&1 || true

echo "[3/14] Build controller image"
(
  cd "$ROOT_DIR"
  sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" build --no-cache  -f cmd/controller/Dockerfile -t "$CONTROLLER_IMAGE" .
)

echo "[4/14] Build collector image"
(
  cd "$ROOT_DIR"
  sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" build --no-cache  -f cmd/collector/Dockerfile -t "$COLLECTOR_IMAGE" .
)

echo "[5/14] Record local image digests/IDs"
sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" images --digests | tee "$LOG_DIR/nerdctl-images.txt"
sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" image inspect "$CONTROLLER_IMAGE" > "$LOG_DIR/controller-image-inspect.json"
sudo nerdctl --address "$K3S_CTR_SOCK" --namespace "$CTR_NS" image inspect "$COLLECTOR_IMAGE" > "$LOG_DIR/collector-image-inspect.json"

echo "[6/14] Recreate operator resources"
(
  cd "$ROOT_DIR"
  kubectl apply -f config/crd/bases
  kubectl apply -f config/collector/collector-daemonset.yaml
  kubectl apply -f config/controller/controller-deployment.yaml
)

echo "[7/14] Wait for rollout"
kubectl -n "$OPS_NS" rollout status ds/checkpoint-collector --timeout=180s
kubectl -n "$OPS_NS" rollout status deploy/forensic-controller --timeout=180s

echo "[8/14] Capture runtime imageIDs from running pods"
kubectl -n "$OPS_NS" get pods -o wide | tee "$LOG_DIR/pods-after-rollout.txt"
kubectl -n "$OPS_NS" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{range .status.containerStatuses[*]}  image={.image}{"\n"}  imageID={.imageID}{"\n"}{end}{end}' | tee "$LOG_DIR/pod-imageids.txt"

echo "[9/14] Ensure test pod exists"
if ! kubectl -n "$APP_NS" get pod "$TEST_POD" >/dev/null 2>&1; then
  kubectl -n "$APP_NS" run "$TEST_POD" --image=ubuntu:24.04 --restart=Never -- sleep infinity
  kubectl -n "$APP_NS" wait --for=condition=Ready "pod/$TEST_POD" --timeout=120s
fi
kubectl -n "$APP_NS" get pod "$TEST_POD" -o wide | tee "$LOG_DIR/test-pod.txt"

echo "[10/14] Trigger snapshot annotation"
kubectl -n "$APP_NS" annotate pod "$TEST_POD" forensics.cybernet.dev/capture=true forensics.cybernet.dev/containers="$TEST_CONTAINER" --overwrite

BEFORE_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "[11/14] Wait for latest snapshot to reach terminal phase"
SNAP_NAME=""
SNAP_PHASE=""
for i in $(seq 1 "$WAIT_SNAPSHOT_SECS"); do
  LINE="$(kubectl get "$CRD" -A --no-headers --sort-by=.metadata.creationTimestamp | tail -n 1 || true)"
  SNAP_NS_CUR="$(echo "$LINE" | awk '{print $1}')"
  SNAP_NAME_CUR="$(echo "$LINE" | awk '{print $2}')"
  if [ -n "$SNAP_NAME_CUR" ]; then
    PHASE="$(kubectl get "$CRD" "$SNAP_NAME_CUR" -n "$SNAP_NS_CUR" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [ "$PHASE" = "Sealed" ] || [ "$PHASE" = "Failed" ]; then
      SNAP_NAME="$SNAP_NAME_CUR"
      SNAP_PHASE="$PHASE"
      SNAP_NS="$SNAP_NS_CUR"
      break
    fi
  fi
  sleep 1
done

if [ -z "${SNAP_NAME:-}" ]; then
  echo "Snapshot did not reach terminal state in ${WAIT_SNAPSHOT_SECS}s" >&2
  kubectl get "$CRD" -A | tee "$LOG_DIR/snapshots-timeout.txt"
  exit 1
fi

echo "Latest snapshot: $SNAP_NS/$SNAP_NAME phase=$SNAP_PHASE"

kubectl get "$CRD" -A --sort-by=.metadata.creationTimestamp | tee "$LOG_DIR/snapshots-table.txt"
kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o yaml > "$LOG_DIR/${SNAP_NAME}.yaml"
FAIL_REASON="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.failureReason}' 2>/dev/null || true)"
BUNDLE_PATH="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.bundlePath}' 2>/dev/null || true)"
EXPECTED_SHA="$(kubectl get "$CRD" "$SNAP_NAME" -n "$SNAP_NS" -o jsonpath='{.status.sha256}' 2>/dev/null || true)"

echo "FailureReason: $FAIL_REASON" | tee "$LOG_DIR/snapshot-result.txt"
echo "BundlePath: $BUNDLE_PATH" | tee -a "$LOG_DIR/snapshot-result.txt"
echo "ExpectedSHA: $EXPECTED_SHA" | tee -a "$LOG_DIR/snapshot-result.txt"

echo "[12/14] Capture logs since trigger"
kubectl -n "$OPS_NS" logs deploy/forensic-controller --since-time="$BEFORE_TS" > "$LOG_DIR/controller-since-trigger.log" || true
kubectl -n "$OPS_NS" logs -l app=checkpoint-collector --since-time="$BEFORE_TS" > "$LOG_DIR/collector-since-trigger.log" || true

grep -Ei "checkpoint|criu|collector|bundle|error|failed|unavailable|sha" "$LOG_DIR/controller-since-trigger.log" | tail -n 200 > "$LOG_DIR/controller-highlights.log" || true
grep -Ei "processed checkpoint|bundlePath|copy|rename|stat|error|sha" "$LOG_DIR/collector-since-trigger.log" | tail -n 200 > "$LOG_DIR/collector-highlights.log" || true

echo "[13/14] Verify bundle if sealed"
if [ "$SNAP_PHASE" = "Sealed" ] && [ -n "$BUNDLE_PATH" ]; then
  CTRL_POD="$(kubectl -n "$OPS_NS" get pods -l app=forensic-controller -o jsonpath='{.items[0].metadata.name}')"
  kubectl -n "$OPS_NS" exec "$CTRL_POD" -- sh -c "ls -lh '$BUNDLE_PATH'; sha256sum '$BUNDLE_PATH'" | tee "$LOG_DIR/bundle-remote-check.txt"

  mkdir -p "$ROOT_DIR/downloads"
  LOCAL_FILE="$ROOT_DIR/downloads/${SNAP_NAME}.bundle.tar.gz"
  kubectl cp "$OPS_NS/$CTRL_POD:$BUNDLE_PATH" "$LOCAL_FILE"
  LOCAL_SHA="$(sha256sum "$LOCAL_FILE" | awk '{print $1}')"
  {
    echo "LOCAL_FILE=$LOCAL_FILE"
    echo "LOCAL_SHA=$LOCAL_SHA"
    echo "EXPECTED_SHA=$EXPECTED_SHA"
  } | tee "$LOG_DIR/bundle-local-check.txt"

  if [ -n "$EXPECTED_SHA" ] && [ "$LOCAL_SHA" != "$EXPECTED_SHA" ]; then
    echo "Checksum mismatch" | tee -a "$LOG_DIR/bundle-local-check.txt"
  fi

  if tar -tzf "$LOCAL_FILE" >/dev/null 2>&1; then
    tar -tzf "$LOCAL_FILE" | head -n 100 > "$LOG_DIR/bundle-entries.txt"
  fi
fi

echo "[14/14] Host/runtime debug (best effort)"
{
  kubectl get nodes -o wide
  kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{" kubelet="}{.status.nodeInfo.kubeletVersion}{" runtime="}{.status.nodeInfo.containerRuntimeVersion}{" kernel="}{.status.nodeInfo.kernelVersion}{"\n"}{end}'
} > "$LOG_DIR/node-runtime.txt" || true

if command -v sudo >/dev/null 2>&1; then
  sudo journalctl -u k3s -S "-30 min" | grep -Ei "checkpoint|criu|runc|containerd|error|fail" | tail -n 400 > "$LOG_DIR/k3s-runtime.log" || true
fi

echo "Done. All artifacts in: $LOG_DIR"
