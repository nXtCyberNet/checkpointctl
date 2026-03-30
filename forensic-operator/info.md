# forensic-operator Troubleshooting and Progress Log

Last updated: 2026-03-30

This file now captures all major issues seen so far, the fix or workaround used, and the current state.

## 1) Wrong module import paths (`yourusername/...` placeholders)
- Symptom:
  - Build failed with missing module/provider errors.
- Root cause:
  - Placeholder import paths remained in generated files.
- Fix:
  - Replaced imports with `github.com/nxtcybernet/checkpointctl/...`.
- Status: Fixed.

## 2) Capture -> bundle type mismatch
- Symptom:
  - `[]string` could not be used where `[]v1alpha1.ContainerCheckpointed` was required.
- Root cause:
  - Bundle request uses typed container checkpoint records.
- Fix:
  - Passed `prefetchResult.ContainersCheckpointed`.
- Status: Fixed.

## 3) Wrong Pod node field
- Symptom:
  - `pod.Status.NodeName` compile error.
- Root cause:
  - Correct field is `pod.Spec.NodeName`.
- Fix:
  - Updated metadata/prefetch logic.
- Status: Fixed.

## 4) Wrong BuildRequest type reference
- Symptom:
  - Unresolved or incorrect build request type at callsite.
- Root cause:
  - `BuildRequest` belongs to bundle package.
- Fix:
  - Switched to `bundle.BuildRequest` and aligned arguments/signature.
- Status: Fixed.

## 5) Bundle package import/coupling problems
- Symptom:
  - Import and package loading issues around `internal/bundle`.
- Root cause:
  - Stale imports and unnecessary coupling.
- Fix:
  - Removed stale imports and simplified bundle types.
- Status: Fixed.

## 6) Mixed packages in one folder
- Symptom:
  - `found packages collector and controller in internal/collector`.
- Root cause:
  - `podwatcher.go` was in wrong directory/package.
- Fix:
  - Moved watcher code to `internal/controller/podwatcher.go`.
- Status: Fixed.

## 7) controller-runtime options API drift
- Symptom:
  - Unknown manager option fields (`MetricsBindAddress`, `Port`).
- Root cause:
  - Version/API mismatch with current controller-runtime.
- Fix:
  - Updated manager setup to current options (`Metrics`, webhook server config).
- Status: Fixed.

## 8) Reconciler wiring and visibility
- Symptom:
  - Missing method and unexported field injection problems.
- Root cause:
  - Wiring expected non-exported fields/methods.
- Fix:
  - Exported injected engine field and switched to available prefetch path.
- Status: Fixed.

## 9) CRD not installed
- Symptom:
  - Runtime error: no matches for kind `ForensicSnapshot`.
- Root cause:
  - CRD not yet applied.
- Fix:
  - Applied CRD manifests directly.
- Status: Fixed.

## 10) CRD schema too strict at creation time
- Symptom:
  - `containersCheckpointed` required before prefetch could populate it.
- Root cause:
  - Field incorrectly required in schema for current flow.
- Fix:
  - Made field optional with `omitempty` and updated CRD schema.
- Status: Fixed.

## 11) RBAC API group mismatch
- Symptom:
  - Forbidden errors for listing/watching ForensicSnapshot resources.
- Root cause:
  - RBAC used old API group (`forensics.checkpointctl.io`).
- Fix:
  - Updated markers/manifests to `forensics.cybernet.dev` and reapplied RBAC.
- Status: Fixed.

## 12) Build context and Docker path issues
- Symptom:
  - Docker build failed with missing directory errors for cmd paths.
- Root cause:
  - Build context assumed wrong project root.
- Fix:
  - Standardized builds from `forensic-operator` root and adjusted Dockerfiles.
- Status: Fixed.

## 13) Kustomize/network fetch failures
- Symptom:
  - TLS handshake timeout while downloading kustomize assets.
- Root cause:
  - Network/proxy instability.
- Fix:
  - Applied CRDs/manifests directly with kubectl as fallback.
- Status: Workaround applied.

## 14) Snapshot trigger annotation command errors
- Symptom:
  - kubectl parse/target errors from command typos.
- Root cause:
  - Resource/container name mistakes and malformed command.
- Fix:
  - Re-ran with correct one-line annotate command.
- Status: Fixed.

## 15) Invalid container selection in snapshot annotation
- Symptom:
  - Prefetch rejected requested container `nginx` as not found.
- Root cause:
  - Actual pod container name was different (`test-app`).
- Fix:
  - Re-annotated with valid container or omitted explicit selection.
- Status: Fixed.

## 16) Cluster/runtime checkpoint endpoint support mismatch
- Symptom:
  - Kubelet checkpoint API endpoint unavailable on some environments.
- Root cause:
  - Runtime/cluster capability gap (environment limitation, not controller bug).
- Fix:
  - Moved testing to environment where endpoint is available (k3s path).
- Status: Environment-dependent, partially mitigated.

## 17) CRIU dump failure due to BusyBox tar in k3s runtime path
- Symptom:
  - Checkpoint reached runtime stage but failed in CRIU dump path.
  - CRIU log reported BusyBox tar incompatibility (`no-unquote`).
- Root cause:
  - k3s bundled runtime toolchain used BusyBox tar for checkpoint path.
- Fix/workaround:
  - Pointed k3s runtime tar path to GNU tar and restarted k3s/workloads.
- Status: Workaround available; environment-specific risk remains.

## 18) Collector service DNS/endpoint not reachable
- Symptom:
  - Controller failed to reach collector service by in-cluster DNS name.
- Root cause:
  - Service object missing or not matching collector pods.
- Fix:
  - Added collector Service manifest and ensured selector/port alignment.
- Status: Fixed.

## 19) Collector auth/token mismatch
- Symptom:
  - Collector returned unauthorized/failed trigger behavior.
- Root cause:
  - Controller used hardcoded/default token path not aligned with deployed secret/env.
- Fix:
  - Switched bundle trigger auth to env-backed token and wired deployment env from secret.
- Status: Fixed.

## 20) Collector trigger 500 from file finalization path
- Symptom:
  - Collector returned 500 while persisting bundle artifacts.
- Root cause:
  - Rename/move assumptions broke across filesystem/device boundaries in some mounts.
- Fix:
  - Hardened handler with stage+rename and copy fallback, plus cleanup/error logs.
- Status: Improved; needs final live verification with newest image.

## 21) Image pull/source mismatch in local cluster tooling
- Symptom:
  - Cluster could not pull expected local images after rebuild.
- Root cause:
  - Images built in one tool/namespace (Docker vs nerdctl/containerd namespace) but cluster looked in another.
- Fix:
  - Standardized build/load path per environment and redeployed workloads.
- Status: Operationally managed; recurring operator risk.

## 22) Docker build instability from inconsistent vendoring
- Symptom:
  - Build errors: modules required in go.mod not marked explicit in vendor/modules.txt.
- Root cause:
  - Vendor metadata drift and environment/network variance.
- Fix applied now:
  - Updated controller/collector Dockerfiles to use module mode build (`-mod=mod`), set GOPROXY, and retry `go mod download`.
- Status: Mitigation applied; pending confirmation in fresh rebuild.

## 23) Docker build download flakiness
- Symptom:
  - Intermittent module download failures/timeouts during image build.
- Root cause:
  - External network/proxy instability during dependency fetch.
- Fix:
  - Added retry loops for dependency download in Dockerfiles.
- Status: Mitigation applied.

## Progress so far
- Code-level compile and wiring issues: Mostly resolved.
- CRD/RBAC/controller startup: Resolved and stable.
- Pod watcher trigger path: Working.
- Capture prefetch/metadata flow: Working.
- Collector reachability and auth wiring: Fixed in manifests/code.
- Collector robustness for bundle finalization: Improved in code.
- Build/deploy pipeline: Improved, but still the most sensitive part due to environment differences and network.

## Current state (what is done vs what is pending)
- Done:
  - Core controller and collector logic corrections.
  - CRD, RBAC, service and deployment wiring fixes.
  - Main runtime and integration error handling improvements.
- Pending confirmation:
  - Rebuild and redeploy both images with latest Dockerfile changes.
  - Run a clean end-to-end snapshot and verify collector no longer returns 500.
  - Confirm final snapshot phase reaches success in target k3s environment.

## Notes and recommendations
- Keep this file as the single incident ledger for reproducibility.
- After each deploy attempt, append:
  - exact image tags used,
  - deployment timestamp,
  - one-line outcome (Success or Failed + reason).
- If failures continue, treat build/publish/deploy chain as first suspect before debugging business logic again.
