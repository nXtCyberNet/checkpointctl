package bundle

import (
	forensicsv1alpha1 "github.com/nxtcybernet/checkpointctl/api/v1alpha1"
	"k8s.io/client-go/kubernetes"
)

// BuildRequest is what the engine passes to the bundle builder
type BuildRequest struct {
	SnapshotName       string
	Namespace          string
	PodName            string
	NodeName           string
	CheckpointPaths    []string // paths returned by Kubelet
	Metadata           any
	SelectedContainers []forensicsv1alpha1.ContainerCheckpointed
	CollectorURL       string // e.g. http://checkpoint-collector:8080
}

// BuildResult is returned to the engine
type BuildResult struct {
	BundlePath              string
	SHA256                  string
	CheckpointDurationMs    int64
	MetadataFetchDurationMs int64
	BundleSizeBytes         int64
	ContainersCheckpointed  []forensicsv1alpha1.ContainerCheckpointed
}

// Builder assembles the final forensic bundle
type Builder struct {
	client kubernetes.Interface
}
