package localllm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	latestVersionAPIURL   = "https://api.github.com/repos/hybridgroup/llama-cpp-builder/releases/latest"
	fetchLatestVersionFunc = fetchLatestRuntimeVersion
)

func LatestRuntimeVersion(ctx context.Context) (string, error) {
	return fetchLatestVersionFunc(ctx)
}

func fetchLatestRuntimeVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestVersionAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("latest runtime version lookup failed with status %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("latest runtime version response missing tag_name")
	}
	return payload.TagName, nil
}
