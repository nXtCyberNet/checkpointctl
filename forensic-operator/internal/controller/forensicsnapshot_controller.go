package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	forensicsv1alpha1 "github.com/nxtcybernet/checkpointctl/api/v1alpha1"
	"github.com/nxtcybernet/checkpointctl/internal/capture"
)

// ForensicSnapshotReconciler reconciles a ForensicSnapshot object
type ForensicSnapshotReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	CaptureEngine *capture.Engine
}

const (
	finalizerName = "forensics.checkpointctl.io/bundle-integrity-confirmed"
)

// +kubebuilder:rbac:groups=forensics.cybernet.dev,resources=forensicsnapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forensics.cybernet.dev,resources=forensicsnapshots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=forensics.cybernet.dev,resources=forensicsnapshots/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core,resources=nodes/proxy,verbs=create
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list
// +kubebuilder:rbac:groups=core,resources=events,verbs=list
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list

func (r *ForensicSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var snapshot forensicsv1alpha1.ForensicSnapshot
	if err := r.Get(ctx, req.NamespacedName, &snapshot); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion + finalizer
	if snapshot.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(&snapshot, finalizerName) {
			controllerutil.RemoveFinalizer(&snapshot, finalizerName)
			if err := r.Update(ctx, &snapshot); err != nil {
				if errors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if missing
	if !controllerutil.ContainsFinalizer(&snapshot, finalizerName) {
		controllerutil.AddFinalizer(&snapshot, finalizerName)
		if err := r.Update(ctx, &snapshot); err != nil {
			if errors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Skip if already done
	if snapshot.Status.Phase == "Sealed" || snapshot.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	// Initialize status on first reconciliation
	if snapshot.Status.Phase == "" {
		snapshot.Status.Phase = "Pending"
		snapshot.Status.StartedAt = metav1.Now()
		snapshot.Status.CaptureID = fmt.Sprintf("fc-%s", time.Now().Format("20060102150405"))
		if err := r.Status().Update(ctx, &snapshot); err != nil {
			if errors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Main capture logic
	return r.reconcileCapture(ctx, &snapshot)
}

func (r *ForensicSnapshotReconciler) reconcileCapture(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	switch snapshot.Status.Phase {
	case "Pending":
		return r.doPrefetch(ctx, snapshot)

	case "Prefetching":
		return r.doCapture(ctx, snapshot)

	default:
		// For Capturing/Collecting/Sealing phases we requeue briefly
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

// doPrefetch: Get pod info + apply container selection
func (r *ForensicSnapshotReconciler) doPrefetch(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	snapshot.Status.Phase = "Prefetching"
	if err := r.Status().Update(ctx, snapshot); err != nil {
		if errors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	prefetch, err := r.CaptureEngine.Prefetch(ctx, capture.Request{
		Namespace:           snapshot.Spec.Namespace,
		PodName:             snapshot.Spec.PodName,
		RequestedContainers: snapshot.Spec.RequestedContainers,
		SnapshotName:        snapshot.Name,
		Priority:            snapshot.Spec.Priority,
	})
	if err != nil {
		snapshot.Status.Phase = "Failed"
		snapshot.Status.FailureReason = fmt.Sprintf("prefetch failed: %v", err)
		_ = r.Status().Update(ctx, snapshot)
		return ctrl.Result{}, err
	}

	// Update spec with discovered info
	snapshot.Spec.NodeName = prefetch.NodeName
	snapshot.Spec.PodUID = prefetch.PodUID
	snapshot.Spec.ContainersCheckpointed = prefetch.ContainersCheckpointed

	if err := r.Update(ctx, snapshot); err != nil {
		if errors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil
}

// doCapture: Run the parallel "Frozen Moment" capture
func (r *ForensicSnapshotReconciler) doCapture(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	req := capture.Request{
		Namespace:           snapshot.Spec.Namespace,
		PodName:             snapshot.Spec.PodName,
		NodeName:            snapshot.Spec.NodeName,
		RequestedContainers: snapshot.Spec.RequestedContainers,
		SnapshotName:        snapshot.Name,
		Priority:            snapshot.Spec.Priority,
	}

	result, err := r.CaptureEngine.Execute(ctx, req)
	if err != nil {
		snapshot.Status.Phase = "Failed"
		snapshot.Status.FailureReason = err.Error()
		_ = r.Status().Update(ctx, snapshot)
		return ctrl.Result{}, err
	}

	// Success - mark as Sealed
	snapshot.Status.Phase = "Sealed"
	snapshot.Status.BundlePath = result.BundlePath
	snapshot.Status.SHA256 = result.SHA256
	snapshot.Status.CapturedAt = metav1.Now()
	snapshot.Status.CompletedAt = metav1.Now()
	snapshot.Status.CheckpointDurationMs = result.CheckpointDurationMs
	snapshot.Status.MetadataFetchDurationMs = result.MetadataFetchDurationMs
	snapshot.Status.BundleSizeBytes = result.BundleSizeBytes

	if err := r.Status().Update(ctx, snapshot); err != nil {
		if errors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller
func (r *ForensicSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forensicsv1alpha1.ForensicSnapshot{}).
		Complete(r)
}
