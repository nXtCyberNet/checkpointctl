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

type TriggerRequest struct {
	CheckpointPath string `json:"checkpointPath"`
	BundleKey      string `json:"bundleKey"`
	SnapshotName   string `json:"snapshotName"`
}

type CollectorResponse struct {
	BundlePath string `json:"bundlePath"`
	SHA256     string `json:"sha256"`
}

type Handler struct {
	storagePath string
	authToken   string
}

func NewHandler(storagePath, authToken string) *Handler {
	return &Handler{storagePath: storagePath, authToken: authToken}
}

func (h *Handler) HandleTrigger(w http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(r.Context())
	start := time.Now()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

	destDir := filepath.Join(h.storagePath, req.BundleKey)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		logger.Error(err, "Failed to create destination directory", "path", destDir)
		http.Error(w, "mkdir failed", http.StatusInternalServerError)
		return
	}

	finalCheckpointPath := filepath.Join(destDir, filepath.Base(req.CheckpointPath))

	// Stage in the same directory so os.Rename is always on-device and thus atomic. This also ensures the final file has the same permissions as the dest directory (important when /var/lib/forensics is mounted with restrictive permissions).
	tempPath := filepath.Join(destDir, fmt.Sprintf(".%s.tmp", filepath.Base(req.CheckpointPath)))

	if err := copyFile(req.CheckpointPath, tempPath); err != nil {
		logger.Error(err, "Failed to copy checkpoint file", "src", req.CheckpointPath)
		http.Error(w, "copy failed", http.StatusInternalServerError)
		return
	}

	sha256sum, err := computeSHA256(tempPath)
	if err != nil {
		_ = os.Remove(tempPath)
		logger.Error(err, "Failed to compute SHA256")
		http.Error(w, "hash failed", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tempPath, finalCheckpointPath); err != nil {
		// Cross-device fallback (defensive; both paths are in destDir so this
		// should never trigger, but guard it anyway)
		if copyErr := copyFile(tempPath, finalCheckpointPath); copyErr != nil {
			logger.Error(err, "Rename failed and fallback copy also failed",
				"renameErr", err, "copyErr", copyErr)
			_ = os.Remove(tempPath)
			http.Error(w, "move failed", http.StatusInternalServerError)
			return
		}
		_ = os.Remove(tempPath)
	}

	// Best-effort cleanup of the original kubelet checkpoint file.
	// Will fail when /var/lib/kubelet/checkpoints is mounted read-only — that
	// is acceptable; log it and continue. , based on rbac rules
	if err := os.Remove(req.CheckpointPath); err != nil {
		logger.Error(err, "Failed to remove original checkpoint file", "path", req.CheckpointPath)
	}

	logger.Info("Collector successfully processed checkpoint",
		"originalPath", req.CheckpointPath,
		"bundlePath", finalCheckpointPath,
		"sha256", sha256sum,
		"durationMs", time.Since(start).Milliseconds())

	resp := CollectorResponse{
		BundlePath: finalCheckpointPath,
		SHA256:     sha256sum,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error(err, "Failed to encode response")
	}
}

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
