package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// triggerKubeletCheckpoint calls the Kubelet Checkpoint API through the apiserver proxy
// This is the secure and recommended way (authenticated via Kubernetes RBAC).
func (e *Engine) triggerKubeletCheckpoint(ctx context.Context, req Request, containers []string) ([]string, error) {
	logger := log.FromContext(ctx)

	if req.NodeName == "" {
		return nil, fmt.Errorf("nodeName is required for kubelet checkpoint")
	}

	var checkpointPaths []string

	for _, container := range containers {
		path := fmt.Sprintf("/api/v1/nodes/%s/proxy/checkpoint/%s/%s/%s",
			req.NodeName, req.Namespace, req.PodName, container)

		logger.Info("Triggering CRIU checkpoint via Kubelet API",
			"container", container,
			"node", req.NodeName,
			"path", path)

		// Use the REST client to call through apiserver proxy
		result, err := e.client.CoreV1().RESTClient().
			Post().
			AbsPath(path).
			DoRaw(ctx)

		if err != nil {
			if strings.Contains(err.Error(), "could not find the requested resource") {
				return nil, fmt.Errorf(
					"kubelet checkpoint API endpoint not available for container %s on node %s; ensure kubelet feature gate ContainerCheckpoint is enabled and container runtime supports checkpoints (original error: %w)",
					container, req.NodeName, err,
				)
			}
			return nil, fmt.Errorf("kubelet checkpoint failed for container %s: %w", container, err)
		}

		// Kubelet response format: { "items": ["/var/lib/kubelet/checkpoints/checkpoint-...tar"] }
		var resp struct {
			Items []string `json:"items"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse kubelet response: %w", err)
		}

		if len(resp.Items) == 0 {
			return nil, fmt.Errorf("kubelet returned no checkpoint path for container %s", container)
		}

		checkpointPaths = append(checkpointPaths, resp.Items...)
	}

	return checkpointPaths, nil
}
