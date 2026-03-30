package capture

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// prefetchPodInfo does the single lightweight GET to solve the "chicken-and-egg" problem
// This is the ONLY sequential call before the two parallel goroutines.
func (e *Engine) prefetchPodInfo(ctx context.Context, namespace, podName string) (*PodInfo, error) {
	logger := log.FromContext(ctx)

	start := time.Now()

	pod, err := e.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod %s/%s: %w", namespace, podName, err)
	}

	if pod.Spec.NodeName == "" {
		return nil, fmt.Errorf("pod %s/%s is not yet scheduled to a node", namespace, podName)
	}

	logger.Info("Pod prefetch completed",
		"nodeName", pod.Spec.NodeName,
		"hostIP", pod.Status.HostIP,
		"durationMs", time.Since(start).Milliseconds())

	return &PodInfo{
		Pod:      pod,
		NodeName: pod.Spec.NodeName,
		HostIP:   pod.Status.HostIP,
		UID:      string(pod.UID),
	}, nil
}

// fetchForensicMetadata gathers rich context around the checkpoint (the "story" behind the memory dump)
func (e *Engine) fetchForensicMetadata(ctx context.Context, podInfo *PodInfo) (*ForensicMetadata, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	pod := podInfo.Pod

	meta := &ForensicMetadata{
		PodSpec:         &pod.Spec,
		Labels:          pod.Labels,
		Annotations:     pod.Annotations,
		OwnerReferences: pod.OwnerReferences,
		EnvVars:         make(map[string]string),
		NodeInfo: NodeInfo{
			Name:             pod.Spec.NodeName,
			KernelVersion:    "",                                          // can be enriched later via node object
			KubeletVersion:   "",                                          // can be enriched later
			ContainerRuntime: pod.Status.ContainerStatuses[0].ContainerID, // simplified
		},
	}

	// Extract environment variables from all containers (very useful for forensics)
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if env.Value != "" {
				meta.EnvVars[env.Name] = env.Value
			} else if env.ValueFrom != nil {
				meta.EnvVars[env.Name] = "[FROM_SECRET/CONFIGMAP]"
			}
		}
	}

	// Fetch last 20 events for this pod (timeline)
	events, err := e.client.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
		Limit:         20,
	})
	if err == nil {
		meta.Events = events.Items
	}

	logger.Info("Forensic metadata fetch completed",
		"envVarsCount", len(meta.EnvVars),
		"eventsCount", len(meta.Events),
		"durationMs", time.Since(start).Milliseconds())

	return meta, nil
}
