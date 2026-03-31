package capture

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodInfo holds data fetched during prefetch
type PodInfo struct {
	Pod      *corev1.Pod
	NodeName string
	HostIP   string
	UID      string
}

// ForensicMetadata contains rich context around the checkpoint
type ForensicMetadata struct {
	CapturedAt      metav1.Time
	PodSpec         *corev1.PodSpec
	Labels          map[string]string
	Annotations     map[string]string
	OwnerReferences []metav1.OwnerReference
	EnvVars         map[string]string
	PodStatus       PodStatusSnapshot
	NetworkPolicies []string
	Events          []corev1.Event
	NodeInfo        NodeInfo
	ClusterInfo     ClusterInfo
}

type PodStatusSnapshot struct {
	Phase               corev1.PodPhase
	PodIP               string
	HostIP              string
	StartTime           *metav1.Time
	QOSClass            corev1.PodQOSClass
	ContainerStates     map[string]string
	ContainerRestartCnt map[string]int32
}

// NodeInfo and ClusterInfo can be expanded as needed
type NodeInfo struct {
	Name             string
	KernelVersion    string
	KubeletVersion   string
	ContainerRuntime string
	OSImage          string
	Architecture     string
	OperatingSystem  string
}

type ClusterInfo struct {
	KubernetesVersion string
	CloudProvider     string
}
