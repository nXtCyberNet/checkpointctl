# Forensic Operator: In-Depth Project Documentation

## 1. Architectural Overview & Context
The `forensic-operator` is a specialized, highly privileged Kubernetes controller forming the native execution backbone for the `checkpointctl` suite. Rather than running ad-hoc scripts on nodes, it leverages the Kubernetes control plane to orchestrate, execute, and secure **cryptographically verifiable forensic checkpoints** of running containers without halting the underlying node or pod.

It consists of two primary reconciliation loops, custom resource definitions (CRDs), and a background collector mechanism. 

### Core Use Case
When an arbitrary pod exhibits malicious behavior or anomalous states, security responders can trigger a "frozen moment" capture. The operator communicates securely with the `kubelet` checkpoint API to invoke a CRIU (Checkpoint/Restore In Userspace) dump. It then bundles the filesystem state, memory pages, and process trees, sending them to a secure collector service.

---

## 2. API Reference: The `ForensicSnapshot` CRD
At the heart of the operator is the `ForensicSnapshot` (`forensics.cybernet.dev/v1alpha1`) custom resource.

### Spec Attributes
The `Spec` defines what to capture and how to handle it:
- `podName` & `namespace`: (Required) The target pod to checkpoint.
- `requestedContainers`: (Optional) A specific list of containers inside the pod. If omitted, all supported containers are targeted.
- `trigger`: Metadata recording whether this was triggered via `annotation`, `scheduled`, or `webhook`.
- `storageBackend`: Where the finalized bundle will be stored (e.g., `local`, `s3`).
- `priority`: Handled as `low`, `normal`, or `high` for queuing.

### Status Phase State Machine
The operator drives each snapshot through a strict state machine via the `Status.Phase` field:
1. `Pending`: The resource is registered. The Operator generates a unique `CaptureID` (e.g. `fc-20260330150405`).
2. `Prefetching`: The operator dynamically resolves `NodeName`, `PodUID`, and active container IDs/digests to prepare the capture constraints.
3. `Capturing`: The `CaptureEngine` synchronously invokes the kubelet API to orchestrate the CRIU memory dump.
4. `Collecting`: The raw dumps are shipped to the collector endpoint.
5. `Sealing`: The bundle is tarred, timestamped, and hashed to ensure chain of custody.
6. `Sealed`: Terminal success state. Provides telemetry: `CompletedAt`, `CheckpointDurationMs`, `BundleSizeBytes`, and `SHA256` integrity hashes.
7. `Failed`: Terminal failure state. Populates `FailureReason` for debugging.

---

## 3. Internal Controllers & Workflows

### A. The PodWatcher Controller
The system supports a frictionless "Annotation-Driven flow" for security teams. You do not need to manually write `ForensicSnapshot` YAMLs. The operator includes a `PodWatcher` that monitors all pods in the cluster.

**Triggering a snapshot via kubectl:**
To instantly snapshot a compromised pod:
```sh
kubectl annotate pod compromised-pod-name forensics.checkpointctl.io/capture="true"
```
To specify a specific container (e.g., just the `nginx` container):
```sh
kubectl annotate pod compromised-pod-name forensics.checkpointctl.io/capture="true" \
  forensics.checkpointctl.io/containers="nginx"
```

**Under the Hood:**
1. The `PodWatcher` detects the `forensics.checkpointctl.io/capture` annotation.
2. It dynamically parses `forensics.checkpointctl.io/containers`.
3. It creates a `ForensicSnapshot` CR on your behalf.
4. It safely strips the annotations from the live Pod so the operator does not enter an infinite loop if the pod reschedules.

### B. The ForensicSnapshot Reconciler
This main controller acts upon the `ForensicSnapshot` resource.
- **Finalizers:** Injects the `forensics.checkpointctl.io/bundle-integrity-confirmed` finalizer. This guarantees that a snapshot object cannot be accidentally `kubectl delete`'d out from under the operator while a heavy network transfer of memory assets is occurring.
- **Capture Engine:** Interfaces directly with internal packages (`internal/capture`) to orchestrate network and filesystem boundaries securely, abstracting away underlying socket intricacies with the node.

---

## 4. Deep Dive: Collector & Bundle Mechanics
During the `Sealing` phase, the underlying engine does not just zip files. It generates a forensic bundle.

- **Checkpointed Assets:** Uses `Prefetch()` to identify exact running Process IDs (PIDs) and Image Digests. This ensures the footprint of the container has not morphed between trigger and execution.
- **Collector Endpoint Integration:** Bundles are sent natively via HTTP to the internal Collector Service. If network boundaries fail or token paths mismatched (as noted in historical troubleshooting), the operator enters an error state populated in `Status.FailureReason`.

---

## 5. Deployment & System Requirements 

### Pre-Requisites
Because memory dumping requires extremely low-level kernel interaction, the following environmental prerequisites are hard constraints:
1. **Kubelet Feature Gate**: The target cluster *must* have the `--feature-gates=ContainerCheckpoint=true` flag enabled on the kubelet.
2. **Runtime Support**: 
   - Uses CRI-O or Containerd (such as k3s).
   - *Known k3s limitation:* Some local k3s distros map tar to a BusyBox binary. CRIU requires GNU tar (`no-unquote` support). In such setups, point your runtime's config path to standard GNU tar.
3. RBAC Privileges: The operator requires `core,pods/get,list,watch,update` and `nodes/proxy/create` permissions to marshal Kubelet API invocations effectively.

### Installing for Development
Standard kubebuilder tooling is utilized for multi-architecture builds.

**1. Create the container image:**
```sh
# Ensure you are at the project root
export IMG=<registry>/forensic-operator:latest
make docker-build docker-push IMG=$IMG
```
*(Note: As of modern fixes, the Dockerfile builds in `-mod=mod` with safe retries to prevent go module proxy timeouts)*

**2. Provision the Manifests:**
Generate CRDs and install onto the active kubeconfig context:
```sh
make manifests generate
make install
```

**3. Deploy the application:**
```sh
make deploy IMG=$IMG
```

### Building the Distribution Packages
Rather than expecting users to `make deploy`, you can ship generic manifest bundles or Helm charts.

**Distributable YAML Install File:**
Builds a cohesive Kustomize representation dropped in `dist/install.yaml`
```sh
make build-installer IMG=<registry>/forensic-operator:latest
```

**Helm Chart Conversion:**
Using alpha kubebuilder hooks:
```sh
kubebuilder edit --plugins=helm/v2-alpha
```
Yields a standard `values.yaml` and templates in `dist/chart`, perfectly formatted for production deployments.

---

## 6. Known Issues / Operational Ledger
Based on the `info.md` historical ledger, the following architectural gaps have been addressed or require active awareness:

- **Mixed Modules & Build Stalls:** Fixed by unifying the `github.com/nxtcybernet/checkpointctl` module paths and enabling explicit proxy loops in the Dockerfile.
- **CRD Schema Strictness (The `omitempty` fix):** Historically the schema demanded all Array fields upfront. Since prefetching populates dynamic attributes, fields like `ContainersCheckpointed` are strictly marked `omitempty`. 
- **RBAC API Drift:** The operator operates strictly under the `forensics.cybernet.dev` API Group. Older iterations mapped to `checkpointctl.io`, causing silent `Forbidden` watcher loops.
- **Bundle File Finalization (500 Errors):** Cross-device filesystem renames (`os.Rename`) inside the collector previously crashed. This was mitigated with robust stage+rename and deep copy fallbacks.
