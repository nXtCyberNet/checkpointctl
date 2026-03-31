package capture

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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

func (e *Engine) fetchForensicMetadata(ctx context.Context, podInfo *PodInfo) (*ForensicMetadata, error) {
	logger := log.FromContext(ctx)
	start := time.Now()

	pod := podInfo.Pod

	meta := &ForensicMetadata{
		CapturedAt:      metav1.Now(),
		PodSpec:         &pod.Spec,
		Labels:          pod.Labels,
		Annotations:     pod.Annotations,
		OwnerReferences: pod.OwnerReferences,
		EnvVars:         make(map[string]string),
		PodStatus: PodStatusSnapshot{
			Phase:               pod.Status.Phase,
			PodIP:               pod.Status.PodIP,
			HostIP:              pod.Status.HostIP,
			StartTime:           pod.Status.StartTime,
			QOSClass:            pod.Status.QOSClass,
			ContainerStates:     make(map[string]string),
			ContainerRestartCnt: make(map[string]int32),
		},
		NodeInfo: NodeInfo{
			Name:             pod.Spec.NodeName,
			KernelVersion:    "",
			KubeletVersion:   "",
			ContainerRuntime: "",
		},
	}

	for _, cs := range pod.Status.ContainerStatuses {
		meta.PodStatus.ContainerRestartCnt[cs.Name] = cs.RestartCount
		meta.PodStatus.ContainerStates[cs.Name] = containerStateString(cs.State)
		if meta.NodeInfo.ContainerRuntime == "" && cs.ContainerID != "" {
			meta.NodeInfo.ContainerRuntime = cs.ContainerID
		}
	}

	// Extract environment variables from all containers
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if env.Value != "" {
				meta.EnvVars[env.Name] = env.Value
			} else if env.ValueFrom != nil {
				meta.EnvVars[env.Name] = "[FROM_SECRET/CONFIGMAP]"
			}
		}
	}

	// Fetch last 20 events for this pod (timeline) not completed till now
	events, err := e.client.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name),
		Limit:         20,
	})
	if err == nil {
		meta.Events = events.Items
	}

	if pod.Spec.NodeName != "" {
		node, err := e.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if err == nil {
			meta.NodeInfo.KernelVersion = node.Status.NodeInfo.KernelVersion
			meta.NodeInfo.KubeletVersion = node.Status.NodeInfo.KubeletVersion
			meta.NodeInfo.OSImage = node.Status.NodeInfo.OSImage
			meta.NodeInfo.Architecture = node.Status.NodeInfo.Architecture
			meta.NodeInfo.OperatingSystem = node.Status.NodeInfo.OperatingSystem
			if meta.ClusterInfo.CloudProvider == "" {
				meta.ClusterInfo.CloudProvider = providerFromID(node.Spec.ProviderID)
			}
		}
	}

	if nps, err := e.client.NetworkingV1().NetworkPolicies(pod.Namespace).List(ctx, metav1.ListOptions{}); err == nil {
		for _, np := range nps.Items {
			if policyMatchesPod(np, pod.Labels) {
				meta.NetworkPolicies = append(meta.NetworkPolicies, np.Name)
			}
		}
	}

	if serverVersion, err := e.client.Discovery().ServerVersion(); err == nil {
		meta.ClusterInfo.KubernetesVersion = serverVersion.GitVersion
	}

	if meta.ClusterInfo.CloudProvider == "" {
		meta.ClusterInfo.CloudProvider = "unknown"
	}

	logger.Info("Forensic metadata fetch completed",
		"envVarsCount", len(meta.EnvVars),
		"networkPoliciesCount", len(meta.NetworkPolicies),
		"eventsCount", len(meta.Events),
		"durationMs", time.Since(start).Milliseconds())

	return meta, nil
}

func policyMatchesPod(np networkingv1.NetworkPolicy, podLabels map[string]string) bool {
	selector, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
	if err != nil {
		return false
	}
	if selector.Empty() {
		return true
	}
	return selector.Matches(labels.Set(podLabels))
}

func providerFromID(providerID string) string {
	if providerID == "" {
		return ""
	}
	parts := strings.SplitN(providerID, "://", 2)
	if len(parts) == 2 && parts[0] != "" {
		return parts[0]
	}
	return providerID
}

func containerStateString(state corev1.ContainerState) string {
	switch {
	case state.Running != nil:
		return fmt.Sprintf("running(startedAt=%s)", state.Running.StartedAt.Time.Format(time.RFC3339))
	case state.Waiting != nil:
		return fmt.Sprintf("waiting(reason=%s,message=%s)", state.Waiting.Reason, state.Waiting.Message)
	case state.Terminated != nil:
		return fmt.Sprintf("terminated(reason=%s,exitCode=%d,finishedAt=%s)", state.Terminated.Reason, state.Terminated.ExitCode, state.Terminated.FinishedAt.Time.Format(time.RFC3339))
	default:
		return "unknown"
	}
}
