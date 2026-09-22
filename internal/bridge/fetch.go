package bridge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const DefaultMeshCacheDir = "/tmp/mesh-cache"

// FetchClientFile downloads a file from the client machine over the reverse bridge tunnel.
func FetchClientFile(ctx context.Context, clientPath string, targetDest string) (string, error) {
	session, err := LoadBridgeSession()
	if err != nil {
		return "", fmt.Errorf("no active bridge session found: %w", err)
	}
	if !session.Active || session.BridgePort <= 0 {
		return "", fmt.Errorf("bridge session is inactive or has invalid port")
	}

	if targetDest == "" {
		_ = os.MkdirAll(DefaultMeshCacheDir, 0755)
		base := filepath.Base(clientPath)
		targetDest = filepath.Join(DefaultMeshCacheDir, base)
	} else {
		_ = os.MkdirAll(filepath.Dir(targetDest), 0755)
	}

	reqURL := fmt.Sprintf("http://127.0.0.1:%d/fetch?path=%s", session.BridgePort, url.QueryEscape(clientPath))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build fetch request: %w", err)
	}
	req.Header.Set("X-Mesh-Token", session.Token)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch request failed over bridge port %d: %w", session.BridgePort, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("bridge server returned status %d: %s", resp.StatusCode, string(body))
	}

	outFile, err := os.Create(targetDest)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file %s: %w", targetDest, err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return "", fmt.Errorf("failed to write file content to %s: %w", targetDest, err)
	}

	return targetDest, nil
}
