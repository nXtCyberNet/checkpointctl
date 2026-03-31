package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	forensicsv1alpha1 "github.com/nxtcybernet/checkpointctl/api/v1alpha1"
)

// PodWatcher watches for the special annotation and creates ForensicSnapshot CRs
type PodWatcher struct {
	client.Client
}

// SetupWithManager registers the pod watcher
func (w *PodWatcher) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(w)
}

func (w *PodWatcher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := w.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	const captureAnnotation = "forensics.cybernet.dev/capture"
	const containersAnnotation = "forensics.cybernet.dev/containers"

	if pod.Annotations == nil {
		return ctrl.Result{}, nil
	}

	if _, ok := pod.Annotations[captureAnnotation]; !ok {
		return ctrl.Result{}, nil
	}

	// Extract requested containers (comma-separated)
	var requestedContainers []string
	if containersStr, ok := pod.Annotations[containersAnnotation]; ok && containersStr != "" {
		for _, c := range strings.Split(containersStr, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				requestedContainers = append(requestedContainers, c)
			}
		}
	}

	logger.Info("Detected capture annotation on pod", "pod", pod.Name, "containers", requestedContainers)

	// Create ForensicSnapshot CR
	snapshot := &forensicsv1alpha1.ForensicSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", pod.Name, time.Now().Format("20060102150405")),
			Namespace: pod.Namespace,
		},
		Spec: forensicsv1alpha1.ForensicSnapshotSpec{
			PodName:             pod.Name,
			PodUID:              string(pod.UID),
			Namespace:           pod.Namespace,
			RequestedContainers: requestedContainers,
			Trigger: forensicsv1alpha1.TriggerInfo{
				Type:   "annotation",
				Source: "kubectl-annotate",
			},
			StorageBackend: "local",
			Priority:       "normal",
		},
	}

	// Create the snapshot
	if err := w.Create(ctx, snapshot); err != nil {
		if errors.IsAlreadyExists(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Remove the annotation so we don't trigger again on reschedule
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var freshPod corev1.Pod
		if err := w.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &freshPod); err != nil {
			return err
		}
		if freshPod.Annotations == nil {
			freshPod.Annotations = make(map[string]string)
		}
		delete(freshPod.Annotations, captureAnnotation)
		delete(freshPod.Annotations, containersAnnotation)
		return w.Update(ctx, &freshPod)
	}); err != nil {
		logger.Error(err, "Failed to remove annotation")
	}

	logger.Info("Successfully created ForensicSnapshot from annotation", "snapshot", snapshot.Name)
	return ctrl.Result{}, nil
}
