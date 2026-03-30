package container

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	forensicsv1alpha1 "github.com/nxtcybernet/checkpointctl/api/v1alpha1"
)

// Select returns the final list of container names that should be checkpointed.
// It implements only the first two tiers of the selection policy:
//
// Tier 1 (Highest) → Explicit list from annotation / webhook / CR
// Tier 2          → Containers defined in a linked ForensicReadiness CR (if no explicit list)
// (Tier 3 Smart Auto-Filter is intentionally skipped for PoC)
// Tier 4 (Fallback) → All non-init containers
//
// Returns the container names in the order they should be checkpointed.
func Select(pod *corev1.Pod, requestedContainers []string) []string {
	logger := log.FromContext(context.Background())

	if len(requestedContainers) > 0 {
		// Tier 1: Explicit list (from annotation, webhook, or ForensicSnapshot spec)
		logger.Info("Using Tier 1 - explicit container list", "containers", requestedContainers)
		return requestedContainers
	}

	// TODO: Tier 2 - ForensicReadiness CR (future)
	// For PoC we skip this and go directly to fallback.
	// In a full implementation we would lookup the ForensicReadiness
	// CR referenced by the snapshot and use its .spec.containers list.

	logger.Info("No explicit container list provided → falling back to all non-init containers")
	return allNonInitContainers(pod)
}

// allNonInitContainers returns all containers except init containers.
// This is the final fallback (Tier 4 in the original proposal).
func allNonInitContainers(pod *corev1.Pod) []string {
	var names []string

	for _, c := range pod.Spec.Containers {
		names = append(names, c.Name)
	}

	// Init containers are intentionally excluded (they already ran and exited)
	return names
}

// ValidateSelection checks that the selected containers actually exist in the pod.
// Useful in the controller before triggering capture.
func ValidateSelection(pod *corev1.Pod, selected []string) error {
	existing := make(map[string]bool)
	for _, c := range pod.Spec.Containers {
		existing[c.Name] = true
	}
	for _, c := range pod.Spec.InitContainers {
		existing[c.Name] = true
	}

	for _, name := range selected {
		if !existing[name] {
			return fmt.Errorf("container %q not found in pod %s/%s", name, pod.Namespace, pod.Name)
		}
	}
	return nil
}

// ToCheckpointed converts selected container names into the CRD struct
// (used when updating ForensicSnapshot status)
func ToCheckpointed(selected []string, pod *corev1.Pod) []forensicsv1alpha1.ContainerCheckpointed {
	var result []forensicsv1alpha1.ContainerCheckpointed

	for _, name := range selected {
		// Find image digest if available
		var digest string
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == name && status.ImageID != "" {
				digest = status.ImageID // usually "docker-pullable://...@sha256:..."
				break
			}
		}

		result = append(result, forensicsv1alpha1.ContainerCheckpointed{
			Name:        name,
			ImageDigest: digest,
			// PID will be filled later by the collector / CRIU response
		})
	}
	return result
}
