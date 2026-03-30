package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nxtcybernet/checkpointctl/internal/collector"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// NewBuilder creates a new bundle builder (local storage only for PoC)
func NewBuilder(client kubernetes.Interface) *Builder {
	return &Builder{client: client}
}

// Build performs collection → assembly → manifest creation
func (b *Builder) Build(ctx context.Context, req BuildRequest) (*BuildResult, error, time.Duration) {
	logger := log.FromContext(ctx)
	start := time.Now()

	// 1. Call collector DaemonSet to copy, hash and move the .tar files
	collectedFiles, err := b.callCollector(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("collector failed: %w", err), 0
	}

	// 2. Prepare bundle files
	bundleDir := filepath.Join("/var/lib/forensics", req.Namespace, req.PodName, req.SnapshotName)
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bundle directory: %w", err), 0
	}

	bundlePath := filepath.Join(bundleDir, fmt.Sprintf("forensic-%s.bundle.tar.gz", req.SnapshotName))

	// 3. Create manifest.json (integrity root)
	manifest, err := b.createManifest(collectedFiles, req.Metadata, req.SnapshotName)
	if err != nil {
		return nil, err, 0
	}

	// 4. Write all files to disk and create final .tar.gz
	if err := b.assembleBundle(bundlePath, collectedFiles, manifest, req); err != nil {
		return nil, err, 0
	}

	bundleStat, err := os.Stat(bundlePath)
	if err != nil {
		return nil, err, 0
	}

	logger.Info("Bundle assembled successfully",
		"bundlePath", bundlePath,
		"sizeBytes", bundleStat.Size())

	// Compute final SHA256 of the entire bundle
	sha256sum, err := computeSHA256(bundlePath)
	if err != nil {
		return nil, err, 0
	}

	return &BuildResult{
		BundlePath:              bundlePath,
		SHA256:                  sha256sum,
		CheckpointDurationMs:    0, // filled by engine if needed
		MetadataFetchDurationMs: 0,
		BundleSizeBytes:         bundleStat.Size(),
		ContainersCheckpointed:  req.SelectedContainers,
	}, nil, time.Since(start)
}

// callCollector talks to the checkpoint-collector DaemonSet
// In internal/bundle/builder.go
func (b *Builder) callCollector(ctx context.Context, req BuildRequest) (map[string]string, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting collector calls", "checkpointCount", len(req.CheckpointPaths), "collectorURL", req.CollectorURL)

	client := &http.Client{Timeout: 60 * time.Second}

	for _, cpPath := range req.CheckpointPaths {
		logger.Info("Triggering collector", "checkpointPath", cpPath, "snapshot", req.SnapshotName)

		payload := collector.TriggerRequest{
			CheckpointPath: cpPath,
			BundleKey:      fmt.Sprintf("%s/%s/%s", req.Namespace, req.PodName, req.SnapshotName),
			SnapshotName:   req.SnapshotName,
		}

		body, _ := json.Marshal(payload)
		httpReq, _ := http.NewRequestWithContext(ctx, "POST", req.CollectorURL+"/trigger", bytes.NewReader(body))
		httpReq.Header.Set("Authorization", "Bearer super-secret-token-change-me-in-production")
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		if err != nil {
			logger.Error(err, "Collector request failed", "checkpointPath", cpPath)
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Info("Collector returned non-OK status", "statusCode", resp.StatusCode, "checkpointPath", cpPath)
			return nil, fmt.Errorf("collector returned %d", resp.StatusCode)
		}

		logger.Info("Collector request succeeded", "checkpointPath", cpPath)
	}

	logger.Info("Collector calls completed", "snapshot", req.SnapshotName)
	return nil, nil // collector already moved the files
}

// createManifest generates manifest.json with per-file SHA256
func (b *Builder) createManifest(files map[string]string, meta any, snapshotName string) (map[string]string, error) {
	manifest := make(map[string]string)

	for bundlePath, realPath := range files {
		sha, err := computeSHA256(realPath)
		if err != nil {
			return nil, err
		}
		manifest[bundlePath] = sha
	}

	// Add metadata.json and ForensicSnapshot.yaml (will be written later)
	manifest["metadata.json"] = "PLACEHOLDER" // will be replaced after writing
	manifest["ForensicSnapshot.yaml"] = "PLACEHOLDER"

	return manifest, nil
}

// assembleBundle creates the final .tar.gz
func (b *Builder) assembleBundle(bundlePath string, files map[string]string, manifest map[string]string, req BuildRequest) error {
	// For PoC we use a simple tar writer (you can use github.com/klauspost/pgzip for better compression)
	// Simplified version - in real code you would create proper tar.gz
	f, err := os.Create(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()

	// TODO: Implement proper tar.gz writing with all files:
	// - checkpoint/*.tar
	// - metadata.json
	// - ForensicSnapshot.yaml (current CR state)
	// - manifest.json

	// For now we just create an empty file so the flow works
	// Replace this with real tar writer in next iteration
	return nil
}

// computeSHA256 is a small helper used everywhere
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
