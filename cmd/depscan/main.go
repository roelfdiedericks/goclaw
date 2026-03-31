package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultPolicyPath = ".depscan.toml"
	defaultProxyBase  = "https://proxy.golang.org"
)

type moduleInfo struct {
	Path     string      `json:"Path"`
	Version  string      `json:"Version"`
	Main     bool        `json:"Main"`
	Indirect bool        `json:"Indirect"`
	Replace  *moduleInfo `json:"Replace,omitempty"`
}

type proxyVersionInfo struct {
	Version string    `json:"Version"`
	Time    time.Time `json:"Time"`
}

type policyConfig struct {
	TrustedPrefixes []string        `toml:"trusted_prefixes"`
	TrustedModules  []string        `toml:"trusted_modules"`
	Allow           []policyAllowed `toml:"allow"`
}

type policyAllowed struct {
	Module  string `toml:"module"`
	Version string `toml:"version"`
	Reason  string `toml:"reason"`
}

type scanConfig struct {
	MinAgeDays              int
	AllowUnresolvedMetadata bool
	Concurrency             int
	PolicyPath              string
	ProxyBaseURL            string
	Now                     time.Time
	FetchVersionInfo        func(context.Context, string, string) (proxyVersionInfo, error)
	RunGoList               func(context.Context) ([]moduleInfo, error)
}

type record struct {
	Status  string
	Module  string
	Version string
	Age     time.Duration
	Reason  string
	Err     error
}

type scanResult struct {
	Records       []record
	Checked       int
	Trusted       int
	Allowed       int
	SkippedLocal  int
	Failed        int
	Errors        int
	ErrorsAllowed int
}

func main() {
	cfg := scanConfig{}
	flag.IntVar(&cfg.MinAgeDays, "min-age-days", 7, "minimum dependency age in days")
	flag.BoolVar(&cfg.AllowUnresolvedMetadata, "allow-unresolved-metadata", false, "warn instead of fail when dependency metadata cannot be resolved")
	flag.IntVar(&cfg.Concurrency, "concurrency", 8, "maximum number of concurrent module proxy lookups")
	flag.StringVar(&cfg.PolicyPath, "policy-path", defaultPolicyPath, "path to depscan TOML policy file")
	flag.StringVar(&cfg.ProxyBaseURL, "proxy-base-url", defaultProxyBase, "base URL for the Go module proxy")
	flag.Parse()

	if err := run(context.Background(), cfg, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "depscan: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg scanConfig, stdout, stderr io.Writer) error {
	if cfg.MinAgeDays <= 0 {
		return fmt.Errorf("min-age-days must be > 0")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now().UTC()
	}
	if cfg.FetchVersionInfo == nil {
		client := &http.Client{Timeout: 15 * time.Second}
		cfg.FetchVersionInfo = fetchProxyVersionInfo(client, cfg.ProxyBaseURL)
	}
	if cfg.RunGoList == nil {
		cfg.RunGoList = loadModuleGraph
	}

	policy, err := loadPolicy(cfg.PolicyPath)
	if err != nil {
		return err
	}

	modules, err := cfg.RunGoList(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Scanning dependency ages (min %d days, concurrency %d)...\n", cfg.MinAgeDays, cfg.Concurrency)
	if cfg.AllowUnresolvedMetadata {
		fmt.Fprintln(stdout, "Note: unresolved metadata override is active.")
	}

	result, err := scanModules(ctx, modules, policy, cfg, func(rec record) {
		renderRecord(stdout, rec, cfg.MinAgeDays, cfg.AllowUnresolvedMetadata)
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout)
	renderSummary(stdout, result)

	if result.Failed > 0 {
		return fmt.Errorf("%d dependencies failed minimum age check (%d days)", result.Failed, cfg.MinAgeDays)
	}
	if result.Errors > 0 && !cfg.AllowUnresolvedMetadata {
		return fmt.Errorf("%d dependencies could not be resolved via module metadata", result.Errors)
	}
	return nil
}

func loadModuleGraph(ctx context.Context) ([]moduleInfo, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("go list -m -json all: %s", msg)
		}
		return nil, fmt.Errorf("go list -m -json all: %w", err)
	}
	return parseModules(&stdout)
}

func parseModules(r io.Reader) ([]moduleInfo, error) {
	dec := json.NewDecoder(r)
	var modules []moduleInfo
	for {
		var mod moduleInfo
		if err := dec.Decode(&mod); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse module stream: %w", err)
		}
		modules = append(modules, mod)
	}
	return modules, nil
}

func loadPolicy(path string) (policyConfig, error) {
	var cfg policyConfig
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read policy file %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return cfg, fmt.Errorf("parse policy file %s: %w", path, err)
	}
	return cfg, nil
}

func fetchProxyVersionInfo(client *http.Client, baseURL string) func(context.Context, string, string) (proxyVersionInfo, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	return func(ctx context.Context, modulePath, version string) (proxyVersionInfo, error) {
		var info proxyVersionInfo
		escapedPath := escapeModulePath(modulePath)
		escapedVersion := url.PathEscape(version)
		reqURL := fmt.Sprintf("%s/%s/@v/%s.info", baseURL, escapedPath, escapedVersion)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return info, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return info, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			if len(body) > 0 {
				return info, fmt.Errorf("proxy returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
			}
			return info, fmt.Errorf("proxy returned %s", resp.Status)
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return info, fmt.Errorf("decode proxy info: %w", err)
		}
		return info, nil
	}
}

type remoteJob struct {
	Module  string
	Version string
}

func scanModules(ctx context.Context, modules []moduleInfo, policy policyConfig, cfg scanConfig, emit func(record)) (scanResult, error) {
	result := scanResult{}
	cutoff := cfg.Now.Add(-time.Duration(cfg.MinAgeDays) * 24 * time.Hour)
	var jobs []remoteJob

	for _, mod := range modules {
		path, version, localReplace := effectiveModule(mod)
		if mod.Main {
			continue
		}
		if path == "" || version == "" {
			continue
		}
		if localReplace {
			rec := record{
				Status:  "LOCAL",
				Module:  path,
				Version: version,
				Reason:  "local replace directive",
			}
			result.Records = append(result.Records, rec)
			result.SkippedLocal++
			if emit != nil {
				emit(rec)
			}
			continue
		}
		if policy.isTrusted(path) {
			rec := record{
				Status:  "TRUST",
				Module:  path,
				Version: version,
				Reason:  "trusted by policy",
			}
			result.Records = append(result.Records, rec)
			result.Trusted++
			if emit != nil {
				emit(rec)
			}
			continue
		}
		if reason, ok := policy.allowedReason(path, version); ok {
			rec := record{
				Status:  "ALLOW",
				Module:  path,
				Version: version,
				Reason:  reason,
			}
			result.Records = append(result.Records, rec)
			result.Allowed++
			if emit != nil {
				emit(rec)
			}
			continue
		}

		jobs = append(jobs, remoteJob{Module: path, Version: version})
	}

	if len(jobs) == 0 {
		return result, nil
	}

	type remoteResult struct {
		record record
	}

	workerCount := cfg.Concurrency
	if workerCount > len(jobs) {
		workerCount = len(jobs)
	}

	jobCh := make(chan remoteJob)
	resultCh := make(chan remoteResult)
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				info, err := cfg.FetchVersionInfo(ctx, job.Module, job.Version)
				if err != nil {
					resultCh <- remoteResult{
						record: record{
							Status:  "ERROR",
							Module:  job.Module,
							Version: job.Version,
							Err:     err,
						},
					}
					continue
				}

				age := cfg.Now.Sub(info.Time)
				if info.Time.After(cutoff) {
					resultCh <- remoteResult{
						record: record{
							Status:  "FAIL",
							Module:  job.Module,
							Version: job.Version,
							Age:     age,
							Reason:  fmt.Sprintf("minimum age is %d days", cfg.MinAgeDays),
						},
					}
					continue
				}

				resultCh <- remoteResult{
					record: record{
						Status:  "OK",
						Module:  job.Module,
						Version: job.Version,
						Age:     age,
					},
				}
			}
		}()
	}

	go func() {
		for _, job := range jobs {
			jobCh <- job
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	for remote := range resultCh {
		rec := remote.record
		result.Checked++
		result.Records = append(result.Records, rec)
		switch rec.Status {
		case "FAIL":
			result.Failed++
		case "ERROR":
			result.Errors++
			if cfg.AllowUnresolvedMetadata {
				result.ErrorsAllowed++
			}
		}
		if emit != nil {
			emit(rec)
		}
	}

	return result, nil
}

func effectiveModule(mod moduleInfo) (string, string, bool) {
	if mod.Replace == nil {
		return mod.Path, mod.Version, false
	}
	if mod.Replace.Version == "" && mod.Replace.Path != "" {
		if filepath.IsAbs(mod.Replace.Path) || strings.HasPrefix(mod.Replace.Path, ".") {
			return mod.Path, mod.Version, true
		}
	}
	if mod.Replace.Path != "" && mod.Replace.Version != "" {
		return mod.Replace.Path, mod.Replace.Version, false
	}
	return mod.Path, mod.Version, false
}

func (p policyConfig) isTrusted(module string) bool {
	for _, item := range p.TrustedModules {
		if strings.TrimSpace(item) == module {
			return true
		}
	}
	for _, prefix := range p.TrustedPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix != "" && strings.HasPrefix(module, prefix) {
			return true
		}
	}
	return false
}

func (p policyConfig) allowedReason(module, version string) (string, bool) {
	for _, entry := range p.Allow {
		if strings.TrimSpace(entry.Module) == module && strings.TrimSpace(entry.Version) == version {
			reason := strings.TrimSpace(entry.Reason)
			if reason == "" {
				reason = "allowed by policy"
			}
			return reason, true
		}
	}
	return "", false
}

func renderRecord(w io.Writer, rec record, minAgeDays int, allowUnresolved bool) {
	switch rec.Status {
	case "OK":
		fmt.Fprintf(w, "OK: %s@%s (%s old)\n", rec.Module, rec.Version, formatAge(rec.Age))
	case "TRUST", "ALLOW", "LOCAL":
		fmt.Fprintf(w, "%s: %s@%s (%s)\n", rec.Status, rec.Module, rec.Version, rec.Reason)
	case "FAIL":
		fmt.Fprintf(w, "FAIL: %s@%s is only %s old (min: %d days)\n", rec.Module, rec.Version, formatAge(rec.Age), minAgeDays)
	case "ERROR":
		if allowUnresolved {
			fmt.Fprintf(w, "ERROR: %s@%s (%v) [allowed by override]\n", rec.Module, rec.Version, rec.Err)
		} else {
			fmt.Fprintf(w, "ERROR: %s@%s (%v)\n", rec.Module, rec.Version, rec.Err)
		}
	}
}

func renderSummary(w io.Writer, result scanResult) {
	fmt.Fprintf(w, "Summary: checked=%d trusted=%d allowed=%d local=%d failed=%d errors=%d\n",
		result.Checked, result.Trusted, result.Allowed, result.SkippedLocal, result.Failed, result.Errors)
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	hours := int(d.Hours() + 0.5)
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := int((d.Hours() / 24) + 0.5)
	return fmt.Sprintf("%dd", days)
}

func escapeModulePath(modulePath string) string {
	var b strings.Builder
	for _, r := range modulePath {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
