# Forensic Operator Detailed Incident Report

Last updated: 2026-03-31

## 1. Scope
This report consolidates architecture context, troubleshooting history, root causes, fixes applied, verification outcomes, and current open risks for the forensic snapshot pipeline.

## 2. Intended End-to-End Flow
1. Pod is annotated for capture.
2. Pod watcher creates a ForensicSnapshot custom resource.
3. Reconciler runs phases:
   - Pending
   - Prefetching
   - Capturing
   - Collecting
   - Sealing
   - Sealed (or Failed)
4. Capture engine:
   - Resolves pod and container selection
   - Calls kubelet checkpoint API
   - Collects forensic metadata
5. Bundle builder:
   - Calls collector endpoint for checkpoint artifact movement/hashing
   - Assembles final forensic bundle archive
   - Computes final bundle SHA256
6. Snapshot status is updated with path/hash/timing fields.

## 3. Major Problems Encountered and Resolved

### 3.1 Build and module path issues
- Symptoms:
  - import failures from placeholder module paths
  - type/signature mismatches between capture and bundle layers
- Root causes:
  - scaffold placeholders and evolving interfaces
- Fixes:
  - unified imports to current module
  - aligned BuildRequest and typed container checkpoint fields
  - fixed node field access (`Spec.NodeName`)

### 3.2 Controller runtime/API drift
- Symptoms:
  - manager option field errors
- Root causes:
  - controller-runtime version/API changes
- Fixes:
  - updated manager option wiring and startup configuration

### 3.3 CRD and RBAC misalignment
- Symptoms:
  - CRD not found
  - forbidden list/watch on ForensicSnapshot
- Root causes:
  - CRD not installed in target cluster
  - old API group in RBAC rules
- Fixes:
  - applied CRD explicitly
  - normalized RBAC to current API group
  - added permissions needed for metadata enrichment (nodes get/list)

### 3.4 Annotation trigger regression
- Symptoms:
  - annotation applied but no snapshot created
- Root causes:
  - watcher only looked for legacy annotation keys
- Fixes:
  - support both new and legacy annotation domains
  - robust comma-separated container parsing
  - remove both key variants after processing

### 3.5 Reconcile conflict noise
- Symptoms:
  - object modified conflict errors in logs
- Root causes:
  - optimistic concurrency races on status/spec updates
- Fixes:
  - treat conflict updates as normal requeue paths instead of hard reconcile errors

### 3.6 Collector network/auth/runtime failures
- Symptoms:
  - collector DNS/service reachability failures
  - auth token mismatch
  - occasional collector 500 on file finalization
- Root causes:
  - missing Service wiring
  - token source mismatch between controller and collector
  - filesystem rename assumptions across mount boundaries
- Fixes:
  - added Service and corrected selectors/ports
  - switched auth to env-backed secret flow
  - added safer move path with fallback behavior

### 3.7 Environment/runtime checkpoint constraints
- Symptoms:
  - checkpoint endpoint unavailable in some clusters
  - CRIU dump failures related to tar behavior in k3s runtime path
- Root causes:
  - environment/runtime capability and tooling differences
- Fixes/workarounds:
  - tested on compatible environment
  - adjusted runtime tar behavior where needed
  - documented as environment-dependent risk

### 3.8 Bundle output bug (critical)
- Symptoms:
  - snapshot showed Sealed but bundle size was 0
  - SHA matched empty-file hash
- Root causes:
  - bundle assembly previously created an empty file placeholder
- Fixes:
  - implemented real tar.gz assembly
  - include metadata and snapshot descriptor files
  - include manifest with hashes

## 4. Why Verification Failed in Some Runs

### 4.1 Wrong pod/path target during verification
- Status bundle path represented controller-side assembled artifact.
- Verification was sometimes attempted from collector pod/path, causing “file not found”.

### 4.2 Context switching issues
- Commands run with elevated shell/context could use different kube config, causing “resource not found” behavior against wrong context.

### 4.3 Partial forensic completeness
- Even after real bundle assembly, checkpoint payload could be marked unavailable.
- Manifest may contain:
  - metadata hash: present
  - snapshot descriptor hash: present
  - checkpoint entry: `UNAVAILABLE`
- This means archive exists, but evidence payload was not fully accessible at assembly time.

## 5. Current Metadata Coverage at Capture Time
Metadata collection is now richer and includes:
- capture timestamp
- pod spec, labels, annotations, owner refs
- pod status snapshot (phase, IPs, start time, QoS)
- per-container state and restart counts
- environment variable map (with valueFrom redaction marker)
- relevant events
- node info enrichment (kernel/kubelet/OS/arch/runtime ID)
- cluster version and cloud provider inference
- matched network policies

Remaining practical limitation:
- completeness still depends on runtime and artifact path accessibility.

## 6. Current Status Summary
What is working:
- CR lifecycle and phase transitions
- annotation-driven snapshot creation
- controller and collector basic integration
- real bundle file assembly (not zero-byte placeholder)
- improved metadata quality
- reduced reconcile conflict noise

What is still risky/open:
- occasional checkpoint payload path availability at sealing time
- environment-specific runtime/CRIU constraints
- build/deploy reliability under network/vendor drift conditions

## 7. Operational Verification Checklist
1. Confirm cluster context and CRD availability.
2. Trigger snapshot via annotation.
3. Ensure newest snapshot enters Sealed or Failed with clear reason.
4. Download latest bundle from the correct runtime location.
5. Verify SHA256 from status vs local file.
6. Inspect archive contents:
   - metadata.json present
   - ForensicSnapshot.yaml present
   - manifest.json present
   - checkpoint payload present and hashed (not UNAVAILABLE)
7. Record run outcome with timestamp and image tags.

## 8. Recommended Next Hardening Steps
1. Fail sealing when any checkpoint payload is unavailable (strict integrity mode).
2. Persist richer immutable run manifest (include component versions and image digests).
3. Add explicit e2e tests for:
   - annotation trigger
   - bundle non-empty guarantee
   - checksum correctness
   - missing payload failure behavior
4. Separate “workflow success” from “forensic completeness success” in status conditions.

## 9. One-Line Conclusion
The control-plane workflow is mostly stable and functional; the remaining high-value work is enforcing strict evidence completeness so `Sealed` always means fully usable forensic payload, not just successful orchestration.# Forensic Operator Detailed Incident Report

Last updated: 2026-03-31

## 1. Scope
This report consolidates architecture context, troubleshooting history, root causes, fixes applied, verification outcomes, and current open risks for the forensic snapshot pipeline.

## 2. Intended End-to-End Flow
1. Pod is annotated for capture.
2. Pod watcher creates a ForensicSnapshot custom resource.
3. Reconciler runs phases:
   - Pending
   - Prefetching
   - Capturing
   - Collecting
   - Sealing
   - Sealed (or Failed)
4. Capture engine:
   - Resolves pod and container selection
   - Calls kubelet checkpoint API
   - Collects forensic metadata
5. Bundle builder:
   - Calls collector endpoint for checkpoint artifact movement/hashing
   - Assembles final forensic bundle archive
   - Computes final bundle SHA256
6. Snapshot status is updated with path/hash/timing fields.

## 3. Major Problems Encountered and Resolved

### 3.1 Build and module path issues
- Symptoms:
  - import failures from placeholder module paths
  - type/signature mismatches between capture and bundle layers
- Root causes:
  - scaffold placeholders and evolving interfaces
- Fixes:
  - unified imports to current module
  - aligned BuildRequest and typed container checkpoint fields
  - fixed node field access (`Spec.NodeName`)

### 3.2 Controller runtime/API drift
- Symptoms:
  - manager option field errors
- Root causes:
  - controller-runtime version/API changes
- Fixes:
  - updated manager option wiring and startup configuration

### 3.3 CRD and RBAC misalignment
- Symptoms:
  - CRD not found
  - forbidden list/watch on ForensicSnapshot
- Root causes:
  - CRD not installed in target cluster
  - old API group in RBAC rules
- Fixes:
  - applied CRD explicitly
  - normalized RBAC to current API group
  - added permissions needed for metadata enrichment (nodes get/list)

### 3.4 Annotation trigger regression
- Symptoms:
  - annotation applied but no snapshot created
- Root causes:
  - watcher only looked for legacy annotation keys
- Fixes:
  - support both new and legacy annotation domains
  - robust comma-separated container parsing
  - remove both key variants after processing

### 3.5 Reconcile conflict noise
- Symptoms:
  - object modified conflict errors in logs
- Root causes:
  - optimistic concurrency races on status/spec updates
- Fixes:
  - treat conflict updates as normal requeue paths instead of hard reconcile errors

### 3.6 Collector network/auth/runtime failures
- Symptoms:
  - collector DNS/service reachability failures
  - auth token mismatch
  - occasional collector 500 on file finalization
- Root causes:
  - missing Service wiring
  - token source mismatch between controller and collector
  - filesystem rename assumptions across mount boundaries
- Fixes:
  - added Service and corrected selectors/ports
  - switched auth to env-backed secret flow
  - added safer move path with fallback behavior

### 3.7 Environment/runtime checkpoint constraints
- Symptoms:
  - checkpoint endpoint unavailable in some clusters
  - CRIU dump failures related to tar behavior in k3s runtime path
- Root causes:
  - environment/runtime capability and tooling differences
- Fixes/workarounds:
  - tested on compatible environment
  - adjusted runtime tar behavior where needed
  - documented as environment-dependent risk

### 3.8 Bundle output bug (critical)
- Symptoms:
  - snapshot showed Sealed but bundle size was 0
  - SHA matched empty-file hash
- Root causes:
  - bundle assembly previously created an empty file placeholder
- Fixes:
  - implemented real tar.gz assembly
  - include metadata and snapshot descriptor files
  - include manifest with hashes

## 4. Why Verification Failed in Some Runs

### 4.1 Wrong pod/path target during verification
- Status bundle path represented controller-side assembled artifact.
- Verification was sometimes attempted from collector pod/path, causing “file not found”.

### 4.2 Context switching issues
- Commands run with elevated shell/context could use different kube config, causing “resource not found” behavior against wrong context.

### 4.3 Partial forensic completeness
- Even after real bundle assembly, checkpoint payload could be marked unavailable.
- Manifest may contain:
  - metadata hash: present
  - snapshot descriptor hash: present
  - checkpoint entry: `UNAVAILABLE`
- This means archive exists, but evidence payload was not fully accessible at assembly time.

## 5. Current Metadata Coverage at Capture Time
Metadata collection is now richer and includes:
- capture timestamp
- pod spec, labels, annotations, owner refs
- pod status snapshot (phase, IPs, start time, QoS)
- per-container state and restart counts
- environment variable map (with valueFrom redaction marker)
- relevant events
- node info enrichment (kernel/kubelet/OS/arch/runtime ID)
- cluster version and cloud provider inference
- matched network policies

Remaining practical limitation:
- completeness still depends on runtime and artifact path accessibility.

## 6. Current Status Summary
What is working:
- CR lifecycle and phase transitions
- annotation-driven snapshot creation
- controller and collector basic integration
- real bundle file assembly (not zero-byte placeholder)
- improved metadata quality
- reduced reconcile conflict noise

What is still risky/open:
- occasional checkpoint payload path availability at sealing time
- environment-specific runtime/CRIU constraints
- build/deploy reliability under network/vendor drift conditions

## 7. Operational Verification Checklist
1. Confirm cluster context and CRD availability.
2. Trigger snapshot via annotation.
3. Ensure newest snapshot enters Sealed or Failed with clear reason.
4. Download latest bundle from the correct runtime location.
5. Verify SHA256 from status vs local file.
6. Inspect archive contents:
   - metadata.json present
   - ForensicSnapshot.yaml present
   - manifest.json present
   - checkpoint payload present and hashed (not UNAVAILABLE)
7. Record run outcome with timestamp and image tags.

## 8. Recommended Next Hardening Steps
1. Fail sealing when any checkpoint payload is unavailable (strict integrity mode).
2. Persist richer immutable run manifest (include component versions and image digests).
3. Add explicit e2e tests for:
   - annotation trigger
   - bundle non-empty guarantee
   - checksum correctness
   - missing payload failure behavior
4. Separate “workflow success” from “forensic completeness success” in status conditions.

## 9. One-Line Conclusion
The control-plane workflow is mostly stable and functional; the remaining high-value work is enforcing strict evidence completeness so `Sealed` always means fully usable forensic payload, not just successful orchestration.

## 10. Targeted Fix Plan For Dump/Sealing Reliability
These actions are high-value and should be applied in this order. They can remove most `UNAVAILABLE` sealing outcomes, but they do not fully remove low-level CRIU runtime dump failures caused by host/runtime incompatibilities.

### 10.1 Make collector return success only after durable final write
- In collector `HandleTrigger`:
  - copy source checkpoint file
  - compute SHA256
  - move to final storage path
  - verify with `os.Stat()` that final file exists and has non-zero size
  - optionally use `Sync()` and a short delay only if filesystem behavior requires it
  - return success only after all above checks pass
- Expected impact:
  - removes false-positive success before file is actually durable/visible

### 10.2 Use collector-returned path as source of truth during sealing
- Store the collector-returned final path immediately in runtime flow/status.
- Bundle assembly must read from that returned path, not reconstruct or infer from kubelet/original path.
- Expected impact:
  - removes most path drift and stale-path failures

### 10.3 Add short retry before marking checkpoint `UNAVAILABLE`
- In Sealing phase, before manifest writes `UNAVAILABLE`:
  - retry up to 3 times
  - 500ms delay between attempts
  - re-run `os.Stat()` and open/read check
- Expected impact:
  - absorbs short propagation races between write and read

### 10.4 Ensure checkpoint path visibility in collector pod
- In collector DaemonSet hostPath volume mounts, set `mountPropagation` (`Bidirectional` or `HostToContainer` as policy allows).
- Expected impact:
  - improves path visibility consistency when host/container mount boundaries are involved

### 10.5 Temporary deep debug logging
- Add logs for:
  - exact checkpoint path returned by kubelet
  - exact path received by collector
  - collector `os.Stat()` result and copy/open errors before and after move
  - exact path consumed by bundle assembler
- Expected impact:
  - quickly isolates path mismatch vs permission vs timing issues

### 10.6 Important limitation
Even with all steps above, failures from CRIU/runtime/toolchain incompatibility (for example host tar/runtime behavior) can still fail the memory dump stage. Those require runtime/environment fixes, not only collector/bundler logic changes.