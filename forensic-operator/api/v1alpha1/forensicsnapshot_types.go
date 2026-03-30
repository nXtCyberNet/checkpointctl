package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=`.spec.podName`
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.spec.nodeName`
// +kubebuilder:printcolumn:name="Containers",type=string,JSONPath=`.spec.containersCheckpointed[*].name`
// +kubebuilder:printcolumn:name="CapturedAt",type=string,JSONPath=`.status.capturedAt`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.failureReason`

// ForensicSnapshot represents a cryptographically verifiable forensic checkpoint of one or more containers.
type ForensicSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ForensicSnapshotSpec   `json:"spec,omitempty"`
	Status ForensicSnapshotStatus `json:"status,omitempty"`
}

// ForensicSnapshotSpec defines which pod and containers to capture.
type ForensicSnapshotSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PodName string `json:"podName"`

	PodUID string `json:"podUID,omitempty"`

	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	NodeName string `json:"nodeName,omitempty"`

	ContainersCheckpointed []ContainerCheckpointed `json:"containersCheckpointed,omitempty"`

	RequestedContainers []string `json:"requestedContainers,omitempty"`

	Trigger TriggerInfo `json:"trigger,omitempty"`

	StorageBackend string `json:"storageBackend,omitempty"`

	BundleKey string `json:"bundleKey,omitempty"`

	// +kubebuilder:validation:Enum=low;normal;high
	Priority string `json:"priority,omitempty"`
}

// TriggerInfo captures how and why this snapshot was triggered
type TriggerInfo struct {
	// +kubebuilder:validation:Enum=annotation;scheduled;webhook
	Type string `json:"type,omitempty"`

	Source string `json:"source,omitempty"`
}

// ContainerCheckpointed holds per-container forensic data
type ContainerCheckpointed struct {
	Name           string      `json:"name"`
	ImageDigest    string      `json:"imageDigest,omitempty"`
	PID            int32       `json:"pid,omitempty"`
	CheckpointedAt metav1.Time `json:"checkpointedAt,omitempty"`
}

// ForensicSnapshotStatus defines the observed state of the capture
type ForensicSnapshotStatus struct {
	// +kubebuilder:validation:Enum=Pending;Prefetching;Capturing;Collecting;Sealing;Sealed;Failed
	Phase string `json:"phase,omitempty"`

	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	CaptureID string `json:"captureID,omitempty"`

	BundlePath string `json:"bundlePath,omitempty"`

	SHA256 string `json:"sha256,omitempty"`

	CapturedAt metav1.Time `json:"capturedAt,omitempty"`

	StartedAt               metav1.Time `json:"startedAt,omitempty"`
	CompletedAt             metav1.Time `json:"completedAt,omitempty"`
	CheckpointDurationMs    int64       `json:"checkpointDurationMs,omitempty"`
	MetadataFetchDurationMs int64       `json:"metadataFetchDurationMs,omitempty"`
	BundleSizeBytes         int64       `json:"bundleSizeBytes,omitempty"`

	RetryCount int32 `json:"retryCount,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`

	FailureReason string `json:"failureReason,omitempty"`
}

// +kubebuilder:object:root=true
type ForensicSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ForensicSnapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ForensicSnapshot{}, &ForensicSnapshotList{})
}
