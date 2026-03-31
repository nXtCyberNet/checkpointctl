# Forensic Operator Detailed Incident Report

Last updated: 2026-03-31

## 1. Scope
This report consolidates architecture context, troubleshooting history, root causes, fixes applied, verification outcomes, and current open risks for the forensic snapshot pipeline.

## 2. Intended End-to-End Flow
1. Pod is annotated for capture.
2. Pod watcher creates a ForensicSnapshot custom resource.
3. Reconciler runs phases: Pending, Prefetching, Capturing, Collecting, Sealing, Sealed or Failed.
4. Capture engine resolves pod/container targets, calls kubelet checkpoint API, and collects metadata.
5. Bundle builder calls collector, assembles final archive, computes SHA256, and updates status.

## 3. Major Problems Encountered and Resolved
1. Module/import/type mismatches during early integration.
2. Controller-runtime API drift and manager option updates.
3. CRD and RBAC API-group mismatches.
4. Annotation trigger mismatches and command typos.
5. Collector DNS/auth wiring issues.
6. Runtime/environment checkpoint constraints (kubelet support, CRIU/toolchain behavior).
7. Bundle assembly bugs: empty/tiny bundles and path visibility gaps.
8. Reconcile conflict races causing duplicate capture attempts.

## 4. Why Verification Failed in Earlier Runs
1. Verification targeted wrong pod/path context in some runs.
2. Mixed kube contexts when using elevated shells.
3. Bundle was marked Sealed while payload path was not truly readable.
4. Duplicate capture race could overwrite bundle and desync SHA status.

## 5. Current Metadata Coverage at Capture Time
Captured metadata now includes pod spec, labels, annotations, owner refs, pod status snapshot, container states/restarts, events, matching network policies, node details, and cluster version fields.

## 6. Current Status Summary
Working now:
1. CR lifecycle and annotation trigger path.
2. Collector/service/auth path.
3. Strict bundle completeness guard.
4. Controller-side/collector-side path alignment with shared storage mount.
5. SHA verification path (remote vs local).

Residual risk:
1. Runtime-level CRIU instability remains environment dependent.
2. Need repeatability runs to confirm no intermittent regressions.

## 7. Operational Verification Checklist
1. Confirm CRD and controller/collector readiness.
2. Trigger snapshot annotation.
3. Verify snapshot reaches terminal phase with meaningful status.
4. Verify bundle exists and has non-trivial size.
5. Verify local SHA equals expected status SHA.
6. Inspect archive entries for checkpoint payload presence.

## 8. Recommended Hardening
1. Keep strict fail-on-missing-payload behavior.
2. Add CI smoke run using reset/redeploy script.
3. Add minimum bundle-size/assertions in test flow.
4. Add explicit duplicate-capture regression test.

## 9. One-Line Conclusion
Workflow is now functionally correct end-to-end with checksum-matched bundles in validated runs; remaining risk is mostly runtime-specific CRIU behavior.

## 10. Targeted Fix Plan For Dump/Sealing Reliability
1. Collector success only after durable final write and path verification.
2. Use collector-returned path as source of truth for bundling.
3. Retry short read windows before declaring payload unavailable.
4. Ensure shared path visibility between collector and controller.
5. Keep targeted debug logs around checkpoint paths and stat/open results.
6. Remember runtime/toolchain incompatibility can still fail memory dump even if collector/bundler logic is correct.

## 11. Latest Validated Success Baseline
Run: `debug-run-20260331-051308`

Validated evidence:
1. Fresh image digests were deployed to running pods.
2. Snapshot `test-app-20260331051612` reached `Sealed`.
3. Bundle path resolved and file size was about 32KB (not tiny metadata-only archive).
4. Remote SHA and local SHA matched exactly:
   - `02070d5c38391a8486e2c6f0aff36c7d65ba05caba525c351b981cd78356c08f`

Interpretation:
1. The previous stale collector-path issue and tiny-bundle false success were addressed in this validated run.