# forensic-operator Troubleshooting Notes

This file tracks issues encountered during bring-up and testing, with root cause and current status.

## 1) Wrong module import paths (`yourusername/...`)
- Symptom:
  - Build errors like: no required module provides package `github.com/yourusername/forensic-checkpoint-controller/...`.
- Root cause:
  - Placeholder imports were left in multiple files.
- Fix:
  - Replaced with `github.com/nxtcybernet/checkpointctl/...`.
- Status: Fixed.

## 2) API type mismatch in capture -> bundle request
- Symptom:
  - `cannot use selectedContainers ([]string) as []v1alpha1.ContainerCheckpointed`.
- Root cause:
  - `bundle.BuildRequest.SelectedContainers` expects typed checkpoint records, not string names.
- Fix:
  - Passed `prefetchResult.ContainersCheckpointed` to bundle build request.
- Status: Fixed.

## 3) `pod.Status.NodeName` compile error
- Symptom:
  - `PodStatus has no field or method NodeName`.
- Root cause:
  - NodeName is on `pod.Spec.NodeName`, not `pod.Status.NodeName`.
- Fix:
  - Updated references in capture metadata/prefetch logic.
- Status: Fixed.

## 4) BuildRequest symbol/use issues
- Symptom:
  - Build call in capture used unresolved/incorrect `BuildRequest`.
- Root cause:
  - Type belongs to `bundle` package.
- Fix:
  - Updated to `bundle.BuildRequest` and adjusted callsite.
- Status: Fixed.

## 5) Circular/import hygiene issues in bundle package
- Symptom:
  - Import metadata/load errors for `internal/bundle`.
- Root cause:
  - Stale placeholder import and tight coupling with capture types.
- Fix:
  - Removed stale imports and decoupled metadata type where needed.
- Status: Fixed.

## 6) Mixed package names in one directory (`internal/collector`)
- Symptom:
  - `found packages collector (...) and controller (...) in internal/collector`.
- Root cause:
  - `podwatcher.go` (package `controller`) was placed under `internal/collector`.
- Fix:
  - Moved pod watcher reconciler to `internal/controller/podwatcher.go`.
- Status: Fixed.

## 7) Controller-runtime options mismatch
- Symptom:
  - Unknown fields in manager options: `MetricsBindAddress`, `Port`.
- Root cause:
  - API changes in current `controller-runtime` version.
- Fix:
  - Switched to `Metrics: metricsserver.Options{...}` and explicit `WebhookServer` options.
- Status: Fixed.

## 8) Reconciler wiring and field visibility issues
- Symptom:
  - Unexported field injection and missing method errors (`PrefetchPodInfo`).
- Root cause:
  - Main was wiring internal field; controller referenced a non-exported/non-existent engine method.
- Fix:
  - Exported injected field (`CaptureEngine`) and switched prefetch flow to engine `Prefetch(...)`.
- Status: Fixed.

## 9) CRD not found at runtime
- Symptom:
  - `no matches for kind "ForensicSnapshot" in version "forensics.cybernet.dev/v1alpha1"`.
- Root cause:
  - CRD was not yet installed.
- Fix:
  - Applied CRD directly from `config/crd/bases/...`.
- Status: Fixed.

## 10) CRD schema too strict for creation-time flow
- Symptom:
  - Potential create-time validation conflict because `spec.containersCheckpointed` was required before prefetch fills it.
- Root cause:
  - Generated schema required field too early.
- Fix:
  - Made `containersCheckpointed` optional (`omitempty`) and updated generated CRD schema accordingly.
- Status: Fixed.

## 11) RBAC API group mismatch
- Symptom:
  - `forensicsnapshots.forensics.cybernet.dev is forbidden` for service account.
- Root cause:
  - RBAC still used old API group `forensics.checkpointctl.io`.
- Fix:
  - Updated RBAC manifests and markers to `forensics.cybernet.dev`; reapplied role/rolebinding and restarted controller.
- Status: Fixed.

## 12) Docker build context/path issues
- Symptom:
  - `stat /app/cmd/collector: directory not found` during Docker build.
- Root cause:
  - Build context/path assumption mismatch between repo root and `forensic-operator` root.
- Fix:
  - Standardized build commands from `forensic-operator` root and adjusted Dockerfiles/commands for test setup.
- Status: Fixed.

## 13) Kustomize download failure during `make install`
- Symptom:
  - TLS handshake timeout fetching `sigs.k8s.io/kustomize/...`.
- Root cause:
  - Network/proxy fetch instability.
- Fix:
  - Bypassed by applying CRD manifest directly with `kubectl apply -f config/crd/bases/...`.
- Status: Workaround applied.

## 14) Pod annotation command mistakes
- Symptom:
  - kubectl parse error from broken resource/name usage and typo.
- Root cause:
  - Command formatting typo (`test-ap`, line-break split).
- Fix:
  - Used single-line annotate command with correct pod/container names.
- Status: Fixed.

## 15) Invalid requested container name in snapshot
- Symptom:
  - `prefetch failed: invalid container selection: container "nginx" not found ...`.
- Root cause:
  - Pod container name was `test-app`, not `nginx`.
- Fix:
  - Re-annotated with valid container (`test-app`) or omitted explicit container selection.
- Status: Fixed.

## 16) Kind/Kubelet checkpoint endpoint not available (current blocker)
- Symptom:
  - Capture fails with:
    - `partial capture rejected - checkpointErr: kubelet checkpoint API endpoint not available ... the server could not find the requested resource`.
- Root cause:
  - Cluster runtime capability issue (endpoint unavailable), not controller logic.
  - Common in Kind/containerd setups unless kubelet feature gate + runtime support are present.
- Fix options:
  - Use a cluster/runtime that supports kubelet checkpoint endpoint.
  - Keep current behavior (fail with clear reason) for unsupported environments.
  - Optional future enhancement: metadata-only fallback mode for non-supporting clusters.
- Status: Open (environment limitation).

## Quick current health summary
- Controller startup: OK
- CRD registration: OK
- RBAC for CRD list/watch: OK
- Pod watcher trigger path: OK
- Metadata collection path: OK
- Checkpoint execution path via kubelet endpoint: Failing due to runtime/endpoint support in current Kind environment.
