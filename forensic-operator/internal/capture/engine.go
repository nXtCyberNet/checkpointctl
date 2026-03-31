package capture

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"

	forensicsv1alpha1 "github.com/nxtcybernet/checkpointctl/api/v1alpha1"
	"github.com/nxtcybernet/checkpointctl/internal/bundle"
	"github.com/nxtcybernet/checkpointctl/internal/container"
)

// Request represents a capture request
type Request struct {
	Namespace           string
	PodName             string
	NodeName            string   // populated during prefetch
	RequestedContainers []string // from annotation/webhook
	SnapshotName        string   // name of ForensicSnapshot CR
	Priority            string   // low|normal|high
}

// Result holds the outcome of a capture operation
type Result struct {
	BundlePath              string
	SHA256                  string
	CheckpointDurationMs    int64
	MetadataFetchDurationMs int64
	BundleSizeBytes         int64
	ContainersCheckpointed  []forensicsv1alpha1.ContainerCheckpointed
}

// PrefetchResult contains data discovered during prefetch.
type PrefetchResult struct {
	PodInfo                *PodInfo
	NodeName               string
	PodUID                 string
	SelectedContainers     []string
	ContainersCheckpointed []forensicsv1alpha1.ContainerCheckpointed
}

// Engine orchestrates the forensic capture process
type Engine struct {
	client        kubernetes.Interface
	bundleBuilder *bundle.Builder
	collectorURL  string // e.g. http://checkpoint-collector.default.svc:8080
}

// NewEngine creates a new capture engine
func NewEngine(client kubernetes.Interface, bundleBuilder *bundle.Builder, collectorURL string) *Engine {
	return &Engine{
		client:        client,
		bundleBuilder: bundleBuilder,
		collectorURL:  collectorURL,
	}
}

// Prefetch resolves pod details and validates requested container selection.
func (e *Engine) Prefetch(ctx context.Context, req Request) (*PrefetchResult, error) {
	podInfo, err := e.prefetchPodInfo(ctx, req.Namespace, req.PodName)
	if err != nil {
		return nil, fmt.Errorf("prefetch pod info failed: %w", err)
	}

	selectedContainers := container.Select(podInfo.Pod, req.RequestedContainers)
	if err := container.ValidateSelection(podInfo.Pod, selectedContainers); err != nil {
		return nil, fmt.Errorf("invalid container selection: %w", err)
	}

	return &PrefetchResult{
		PodInfo:                podInfo,
		NodeName:               podInfo.NodeName,
		PodUID:                 podInfo.UID,
		SelectedContainers:     selectedContainers,
		ContainersCheckpointed: container.ToCheckpointed(selectedContainers, podInfo.Pod),
	}, nil
}

// Execute performs the full capture: prefetch → parallel capture → collection → bundle
func (e *Engine) Execute(ctx context.Context, req Request) (*Result, error) {
	logger := log.FromContext(ctx)
	startTime := time.Now()

	logger.Info("Starting forensic capture", "pod", req.PodName, "namespace", req.Namespace)

	// Phase 1: Prefetch (Chicken-and-Egg solution)
	prefetchStart := time.Now()
	prefetchResult, err := e.Prefetch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("prefetch failed: %w", err)
	}

	podInfo := prefetchResult.PodInfo
	selectedContainers := prefetchResult.SelectedContainers
	req.NodeName = prefetchResult.NodeName

	logger.Info("Prefetch completed",
		"nodeName", req.NodeName,
		"selectedContainers", selectedContainers,
		"durationMs", time.Since(prefetchStart).Milliseconds())

	// Phase 2: Parallel Capture (The Frozen Moment)
	var (
		wg              sync.WaitGroup
		checkpointPaths []string
		forensicMeta    *ForensicMetadata
		checkpointErr   error
		metadataErr     error
	)

	wg.Add(2)

	// Goroutine A: CRIU Checkpoint via Kubelet API
	go func() {
		defer wg.Done()
		checkpointStart := time.Now()

		paths, err := e.triggerKubeletCheckpoint(ctx, req, selectedContainers)
		checkpointErr = err
		checkpointPaths = paths

		if err == nil {
			logger.Info("CRIU checkpoint completed",
				"containers", selectedContainers,
				"durationMs", time.Since(checkpointStart).Milliseconds())
		}
	}()

	// Goroutine B: Full Forensic Metadata
	go func() {
		defer wg.Done()
		metaStart := time.Now()

		meta, err := e.fetchForensicMetadata(ctx, podInfo)
		metadataErr = err
		forensicMeta = meta

		if err == nil {
			logger.Info("Metadata fetch completed", "durationMs", time.Since(metaStart).Milliseconds())
		}
	}()

	wg.Wait()

	// Strict partial failure rejection (as per proposal 4.7)
	if checkpointErr != nil || metadataErr != nil {
		return nil, fmt.Errorf("partial capture rejected - checkpointErr: %v, metadataErr: %v",
			checkpointErr, metadataErr)
	}

	// Phase 3: Collection + Bundle Assembly
	bundleResult, err, bundleDuration := e.bundleBuilder.Build(ctx, bundle.BuildRequest{
		SnapshotName:       req.SnapshotName,
		Namespace:          req.Namespace,
		PodName:            req.PodName,
		NodeName:           req.NodeName,
		CheckpointPaths:    checkpointPaths,
		Metadata:           forensicMeta,
		SelectedContainers: prefetchResult.ContainersCheckpointed,
		CollectorURL:       e.collectorURL,
	})

	if err != nil {
		return nil, fmt.Errorf("bundle assembly failed: %w", err)
	}

	totalDuration := time.Since(startTime).Milliseconds()

	logger.Info("Forensic capture completed successfully",
		"bundlePath", bundleResult.BundlePath,
		"sha256", bundleResult.SHA256,
		"totalDurationMs", totalDuration)

	return &Result{
		BundlePath:              bundleResult.BundlePath,
		SHA256:                  bundleResult.SHA256,
		CheckpointDurationMs:    bundleDuration.Milliseconds(),
		MetadataFetchDurationMs: bundleResult.MetadataFetchDurationMs,
		BundleSizeBytes:         bundleResult.BundleSizeBytes,
		ContainersCheckpointed:  prefetchResult.ContainersCheckpointed,
	}, nil
}
