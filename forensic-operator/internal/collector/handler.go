package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TriggerRequest is what the controller sends to the collector
type TriggerRequest struct {
	CheckpointPath string `json:"checkpointPath"`
	BundleKey      string `json:"bundleKey"` // e.g. production/payments-api/xxx
	SnapshotName   string `json:"snapshotName"`
}

// CollectorResponse is returned to the controller
type CollectorResponse struct {
	BundlePath string `json:"bundlePath"`
	SHA256     string `json:"sha256"`
}

// Handler is the HTTP handler for the checkpoint-collector DaemonSet
type Handler struct {
	storagePath string // usually /var/lib/forensics
	authToken   string // simple pre-shared secret for PoC
}

// NewHandler creates the collector handler
func NewHandler(storagePath, authToken string) *Handler {
	return &Handler{
		storagePath: storagePath,
		authToken:   authToken,
	}
}

// HandleTrigger is the main endpoint: POST /trigger
func (h *Handler) HandleTrigger(w http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(r.Context())
	start := time.Now()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Simple auth check (pre-shared secret from Kubernetes Secret)
	if r.Header.Get("Authorization") != "Bearer "+h.authToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req TriggerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.CheckpointPath == "" {
		http.Error(w, "checkpointPath is required", http.StatusBadRequest)
		return
	}

	// 1. Build final bundle path first so temp staging can happen on the same filesystem.
	bundlePath := filepath.Join(h.storagePath, req.BundleKey, "bundle.tar.gz")
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0755); err != nil {
		http.Error(w, "mkdir failed", http.StatusInternalServerError)
		return
	}

	// Stage temp artifact in destination directory to avoid cross-device rename errors.
	tempPath := filepath.Join(filepath.Dir(bundlePath), fmt.Sprintf(".%s.tmp", filepath.Base(req.CheckpointPath)))

	// 2. Copy the checkpoint .tar to a safe temp location
	if err := copyFile(req.CheckpointPath, tempPath); err != nil {
		logger.Error(err, "Failed to copy checkpoint file")
		http.Error(w, "copy failed", http.StatusInternalServerError)
		return
	}

	// 3. Compute SHA256 before any transmission
	sha256sum, err := computeSHA256(tempPath)
	if err != nil {
		http.Error(w, "hash failed", http.StatusInternalServerError)
		return
	}

	// For PoC we just move the checkpoint tar into the bundle dir
	// (In full version we would create the full .bundle.tar.gz here)
	finalCheckpointPath := filepath.Join(filepath.Dir(bundlePath), filepath.Base(req.CheckpointPath))
	if err := os.Rename(tempPath, finalCheckpointPath); err != nil {
		// Fallback for environments where rename may still fail unexpectedly.
		if copyErr := copyFile(tempPath, finalCheckpointPath); copyErr != nil {
			logger.Error(err, "Rename failed")
			logger.Error(copyErr, "Fallback copy after rename failure also failed")
			http.Error(w, "move failed", http.StatusInternalServerError)
			return
		}
		_ = os.Remove(tempPath)
	}

	// 4. Cleanup original checkpoint file (critical for node disk safety)
	if err := os.Remove(req.CheckpointPath); err != nil {
		logger.Error(err, "Failed to remove original checkpoint file", "path", req.CheckpointPath)
	}

	logger.Info("Collector successfully processed checkpoint",
		"originalPath", req.CheckpointPath,
		"bundlePath", bundlePath,
		"sha256", sha256sum,
		"durationMs", time.Since(start).Milliseconds())

	resp := CollectorResponse{
		BundlePath: bundlePath,
		SHA256:     sha256sum,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// Simple file copy helper
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
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
