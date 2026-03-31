package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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

	// Terminal state guard — this is the primary defence against duplicate
	// CRIU checkpoints when the controller requeues after a conflict on the
	// final status update. If the snapshot is already done, do nothing.
	if snapshot.Status.Phase == "Sealed" || snapshot.Status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}

	// Initialise status on the very first reconciliation
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

	return r.reconcileCapture(ctx, &snapshot)
}

func (r *ForensicSnapshotReconciler) reconcileCapture(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	switch snapshot.Status.Phase {
	case "Pending":
		return r.doPrefetch(ctx, snapshot)
	case "Prefetching":
		return r.doCapture(ctx, snapshot)
	default:
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
}

// doPrefetch resolves pod info and container selection.
func (r *ForensicSnapshotReconciler) doPrefetch(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	// Advance phase first so a requeue cannot re-enter doPrefetch
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

	// Persist discovered spec fields with RetryOnConflict so a stale resource
	// version never silently drops NodeName / PodUID before doCapture reads them.
	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest forensicsv1alpha1.ForensicSnapshot
		if fetchErr := r.Get(ctx, types.NamespacedName{
			Name:      snapshot.Name,
			Namespace: snapshot.Namespace,
		}, &latest); fetchErr != nil {
			return fetchErr
		}
		latest.Spec.NodeName = prefetch.NodeName
		latest.Spec.PodUID = prefetch.PodUID
		latest.Spec.ContainersCheckpointed = prefetch.ContainersCheckpointed
		return r.Update(ctx, &latest)
	})
	if updateErr != nil {
		return ctrl.Result{}, updateErr
	}

	return ctrl.Result{Requeue: true}, nil
}

// doCapture runs the parallel "Frozen Moment" capture.
//
// KEY FIX: all status writes use RetryOnConflict so that a stale-resource-version
// error is resolved in-process instead of via a Requeue. Without this, a conflict
// on the final "Sealed" write requeued the reconciler, which saw phase ==
// "Prefetching" again, bypassed the terminal-state guard, and fired a second
// CRIU checkpoint against the same snapshot.
func (r *ForensicSnapshotReconciler) doCapture(ctx context.Context, snapshot *forensicsv1alpha1.ForensicSnapshot) (ctrl.Result, error) {
	// Acquire capture lock: only the reconcile that successfully flips
	// Prefetching -> Capturing is allowed to execute CRIU/bundle logic.
	transitioned := false
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest forensicsv1alpha1.ForensicSnapshot
		if fetchErr := r.Get(ctx, types.NamespacedName{
			Name:      snapshot.Name,
			Namespace: snapshot.Namespace,
		}, &latest); fetchErr != nil {
			return fetchErr
		}

		// Another reconcile already moved forward or finished.
		if latest.Status.Phase == "Sealed" || latest.Status.Phase == "Failed" || latest.Status.Phase == "Capturing" {
			return nil
		}
		if latest.Status.Phase != "Prefetching" {
			return nil
		}

		latest.Status.Phase = "Capturing"
		if updateErr := r.Status().Update(ctx, &latest); updateErr != nil {
			return updateErr
		}
		transitioned = true
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	if !transitioned {
		// A concurrent reconcile already owns or finished this snapshot.
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}

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
		// Write failure status; RetryOnConflict ensures it lands even if the
		// resource version changed since we last fetched the snapshot.
		_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest forensicsv1alpha1.ForensicSnapshot
			if fetchErr := r.Get(ctx, types.NamespacedName{
				Name:      snapshot.Name,
				Namespace: snapshot.Namespace,
			}, &latest); fetchErr != nil {
				return fetchErr
			}
			if latest.Status.Phase == "Sealed" || latest.Status.Phase == "Failed" {
				return nil // already terminal, nothing to do
			}
			latest.Status.Phase = "Failed"
			latest.Status.FailureReason = err.Error()
			return r.Status().Update(ctx, &latest)
		})
		return ctrl.Result{}, err
	}

	// Write success status with RetryOnConflict + idempotency guard.
	// Re-fetching inside the loop guarantees we apply to the latest resource
	// version. The terminal-state check prevents a second write if two
	// reconciles somehow race to this point.
	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest forensicsv1alpha1.ForensicSnapshot
		if fetchErr := r.Get(ctx, types.NamespacedName{
			Name:      snapshot.Name,
			Namespace: snapshot.Namespace,
		}, &latest); fetchErr != nil {
			return fetchErr
		}
		if latest.Status.Phase == "Sealed" || latest.Status.Phase == "Failed" {
			return nil // idempotency guard — already done
		}
		latest.Status.Phase = "Sealed"
		latest.Status.BundlePath = result.BundlePath
		latest.Status.SHA256 = result.SHA256
		latest.Status.CapturedAt = metav1.Now()
		latest.Status.CompletedAt = metav1.Now()
		latest.Status.CheckpointDurationMs = result.CheckpointDurationMs
		latest.Status.MetadataFetchDurationMs = result.MetadataFetchDurationMs
		latest.Status.BundleSizeBytes = result.BundleSizeBytes
		return r.Status().Update(ctx, &latest)
	})

	// No Requeue on success. Any future requeue for this object hits the
	// terminal-state guard at the top of Reconcile() and exits immediately.
	return ctrl.Result{}, updateErr
}

// SetupWithManager sets up the controller
func (r *ForensicSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forensicsv1alpha1.ForensicSnapshot{}).
		Complete(r)
}
