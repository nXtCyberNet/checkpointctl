package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

	collectedFiles, err := b.callCollector(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("collector failed: %w", err), 0
	}

	bundleDir := filepath.Join("/var/lib/forensics", req.Namespace, req.PodName, req.SnapshotName)
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bundle directory: %w", err), 0
	}

	bundlePath := filepath.Join(bundleDir, fmt.Sprintf("forensic-%s.bundle.tar.gz", req.SnapshotName))

	metadataBytes, err := json.MarshalIndent(req.Metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err), 0
	}

	snapshotDoc := b.snapshotYAML(req)
	manifest, err := b.createManifest(collectedFiles, metadataBytes, []byte(snapshotDoc))
	if err != nil {
		return nil, err, 0
	}

	if err := b.assembleBundle(bundlePath, collectedFiles, manifest, metadataBytes, []byte(snapshotDoc)); err != nil {
		return nil, err, 0
	}

	bundleStat, err := os.Stat(bundlePath)
	if err != nil {
		return nil, err, 0
	}

	logger.Info("Bundle assembled successfully",
		"bundlePath", bundlePath,
		"sizeBytes", bundleStat.Size())

	sha256sum, err := computeSHA256(bundlePath)
	if err != nil {
		return nil, err, 0
	}

	return &BuildResult{
		BundlePath:              bundlePath,
		SHA256:                  sha256sum,
		CheckpointDurationMs:    0,
		MetadataFetchDurationMs: 0,
		BundleSizeBytes:         bundleStat.Size(),
		ContainersCheckpointed:  req.SelectedContainers,
	}, nil, time.Since(start)
}

func (b *Builder) callCollector(ctx context.Context, req BuildRequest) (map[string]string, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting collector calls", "checkpointCount", len(req.CheckpointPaths), "collectorURL", req.CollectorURL)
	authToken := os.Getenv("FORENSICS_AUTH_TOKEN")
	if authToken == "" {
		return nil, fmt.Errorf("FORENSICS_AUTH_TOKEN is not set for controller")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	collectedFiles := make(map[string]string)

	for idx, cpPath := range req.CheckpointPaths {
		logger.Info("Triggering collector", "checkpointPath", cpPath, "snapshot", req.SnapshotName)

		payload := collector.TriggerRequest{
			CheckpointPath: cpPath,
			BundleKey:      fmt.Sprintf("%s/%s/%s", req.Namespace, req.PodName, req.SnapshotName),
			SnapshotName:   req.SnapshotName,
		}

		body, _ := json.Marshal(payload)
		httpReq, _ := http.NewRequestWithContext(ctx, "POST", req.CollectorURL+"/trigger", bytes.NewReader(body))
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
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

		var collectorResp collector.CollectorResponse
		if err := json.NewDecoder(resp.Body).Decode(&collectorResp); err != nil {
			return nil, fmt.Errorf("collector response decode failed: %w", err)
		}

		if collectorResp.BundlePath == "" {
			return nil, fmt.Errorf("collector returned empty bundle path for %s", cpPath)
		}

		archiveName := filepath.Join("checkpoints", fmt.Sprintf("%02d-%s", idx, filepath.Base(collectorResp.BundlePath)))
		collectedFiles[archiveName] = collectorResp.BundlePath

		logger.Info("Collector request succeeded", "checkpointPath", cpPath)
	}

	logger.Info("Collector calls completed", "snapshot", req.SnapshotName)
	return collectedFiles, nil
}

func (b *Builder) createManifest(files map[string]string, metadataBytes, snapshotBytes []byte) (map[string]string, error) {
	manifest := make(map[string]string)

	for bundlePath, realPath := range files {
		if err := waitForReadableFile(realPath, 3, 500*time.Millisecond); err != nil {
			return nil, fmt.Errorf("checkpoint payload unavailable for %s at %s: %w", bundlePath, realPath, err)
		}

		sha, err := computeSHA256(realPath)
		if err != nil {
			return nil, err
		}
		manifest[bundlePath] = sha
	}

	manifest["metadata.json"] = hashBytes(metadataBytes)
	manifest["ForensicSnapshot.yaml"] = hashBytes(snapshotBytes)

	return manifest, nil
}

func (b *Builder) assembleBundle(bundlePath string, files map[string]string, manifest map[string]string, metadataBytes, snapshotBytes []byte) error {
	f, err := os.Create(bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	archivePaths := make([]string, 0, len(files))
	for archivePath := range files {
		archivePaths = append(archivePaths, archivePath)
	}
	sort.Strings(archivePaths)

	for _, archivePath := range archivePaths {
		realPath := files[archivePath]
		if err := addFileToTar(tw, archivePath, realPath); err != nil {
			return fmt.Errorf("failed adding checkpoint payload %s from %s: %w", archivePath, realPath, err)
		}
	}

	if err := addBytesToTar(tw, "metadata.json", metadataBytes); err != nil {
		return err
	}
	if err := addBytesToTar(tw, "ForensicSnapshot.yaml", snapshotBytes); err != nil {
		return err
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := addBytesToTar(tw, "manifest.json", manifestBytes); err != nil {
		return err
	}

	return nil
}

func (b *Builder) snapshotYAML(req BuildRequest) string {
	return fmt.Sprintf(
		"apiVersion: forensics.cybernet.dev/v1alpha1\nkind: ForensicSnapshot\nmetadata:\n  name: %s\n  namespace: %s\nspec:\n  podName: %s\n  nodeName: %s\n",
		req.SnapshotName,
		req.Namespace,
		req.PodName,
		req.NodeName,
	)
}

func addFileToTar(tw *tar.Writer, archivePath, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name:    archivePath,
		Mode:    0644,
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	_, err = io.Copy(tw, f)
	return err
}

func addBytesToTar(tw *tar.Writer, archivePath string, data []byte) error {
	hdr := &tar.Header{
		Name:    archivePath,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func waitForReadableFile(path string, attempts int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		f, err := os.Open(path)
		if err == nil {
			_ = f.Close()
			return nil
		}
		lastErr = err
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return lastErr
}

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
