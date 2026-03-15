package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"
)

const defaultModel = "KnightsAnalytics/all-MiniLM-L6-v2"

type config struct {
	Backend      string
	Model        string
	ModelPath    string
	ModelsDir    string
	OrtLibDir    string
	XLAPluginDir string
	OnnxFilename string
	File         string
	Query        string
	TopK         int
	Bench        bool
	BenchIters   int
	BenchWarmup  int
	BenchSeed    int64
	Normalize    bool
	Verbose      bool
}

type chunk struct {
	Index int
	Text  string
}

type scoredChunk struct {
	Chunk      chunk
	Score      float64
	Dimensions int
}

type setupResult struct {
	InputPath         string
	ModelPath         string
	Chunks            []chunk
	ChunkEmbeddings   [][]float32
	Pipeline          *pipelines.FeatureExtractionPipeline
	Session           *hugot.Session
	SessionElapsed    time.Duration
	PipelineElapsed   time.Duration
	ChunkEmbedElapsed time.Duration
}

type benchmarkResult struct {
	Queries             []string
	WarmupIterations    int
	Iterations          int
	QueryEmbedSamples   []time.Duration
	SearchTotalSamples  []time.Duration
	BestScoreSamples    []float64
	TotalBenchmarkTime  time.Duration
}

func main() {
	cfg := parseFlags()

	setup, err := prepareEmbeddings(cfg)
	if err != nil {
		fail("%v", err)
	}
	defer func() {
		if err := setup.Session.Destroy(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: destroy session: %v\n", err)
		}
	}()

	if cfg.Bench {
		bench, err := runBenchmark(cfg, setup)
		if err != nil {
			fail("run benchmark: %v", err)
		}
		printBenchmark(cfg, setup, bench)
		return
	}

	if strings.TrimSpace(cfg.Query) == "" {
		fail("query is required unless -bench is set; pass -query")
	}

	queryStart := time.Now()
	queryOutput, err := setup.Pipeline.RunPipeline([]string{cfg.Query})
	if err != nil {
		fail("embed query: %v", err)
	}
	queryEmbedElapsed := time.Since(queryStart)

	if len(queryOutput.Embeddings) != 1 {
		fail("expected exactly one query embedding, got %d", len(queryOutput.Embeddings))
	}

	results := rankChunks(setup.Chunks, setup.ChunkEmbeddings, queryOutput.Embeddings[0])
	topK := cfg.TopK
	if topK <= 0 || topK > len(results) {
		topK = len(results)
	}

	printHeader(cfg, setup)
	fmt.Printf("query:        %s\n", cfg.Query)
	fmt.Printf("query embed:  %s\n", queryEmbedElapsed.Round(time.Millisecond))
	fmt.Println()
	fmt.Printf("Top %d results\n", topK)
	fmt.Println()

	for i := 0; i < topK; i++ {
		result := results[i]
		fmt.Printf("%d. score=%.4f dims=%d chunk=%d\n", i+1, result.Score, result.Dimensions, result.Chunk.Index)
		fmt.Printf("   %s\n", indentSnippet(result.Chunk.Text))
		fmt.Println()
	}
}

func parseFlags() config {
	home, _ := os.UserHomeDir()
	defaultFile := filepath.Join(home, ".openclaw", "workspace", "MEMORY.md")
	defaultModelsDir := filepath.Join(home, ".cache", "goclaw", "embtest-models")

	cfg := config{}
	flag.StringVar(&cfg.Backend, "backend", "go", "Backend to use: go, ort, xla")
	flag.StringVar(&cfg.Model, "model", defaultModel, "Hugging Face model repo to download when -model-path is empty")
	flag.StringVar(&cfg.ModelPath, "model-path", "", "Existing local model directory; skips downloading when set")
	flag.StringVar(&cfg.ModelsDir, "models-dir", defaultModelsDir, "Directory where downloaded models are stored")
	flag.StringVar(&cfg.OrtLibDir, "ort-lib-dir", "", "Directory containing the ONNX Runtime shared library for the ORT backend")
	flag.StringVar(&cfg.XLAPluginDir, "xla-plugin-dir", "", "Directory containing the PJRT/XLA plugin for the XLA backend")
	flag.StringVar(&cfg.OnnxFilename, "onnx-filename", "model.onnx", "ONNX filename inside the model directory")
	flag.StringVar(&cfg.File, "file", defaultFile, "Markdown file to ingest")
	flag.StringVar(&cfg.Query, "query", "", "Semantic query to run against embedded chunks")
	flag.IntVar(&cfg.TopK, "topk", 5, "Number of results to print")
	flag.BoolVar(&cfg.Bench, "bench", false, "Run benchmark mode instead of printing search hits")
	flag.IntVar(&cfg.BenchIters, "bench-iters", 500, "Benchmark iterations to record")
	flag.IntVar(&cfg.BenchWarmup, "bench-warmup", 25, "Warmup iterations before benchmark timing")
	flag.Int64Var(&cfg.BenchSeed, "bench-seed", 42, "Random seed for benchmark query sampling")
	flag.BoolVar(&cfg.Normalize, "normalize", true, "Normalize embeddings before similarity scoring")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose model download output")
	flag.Parse()
	return cfg
}

func prepareEmbeddings(cfg config) (*setupResult, error) {
	inputPath, err := expandPath(cfg.File)
	if err != nil {
		return nil, fmt.Errorf("expand input path: %w", err)
	}

	modelPath := cfg.ModelPath
	if modelPath != "" {
		modelPath, err = expandPath(modelPath)
		if err != nil {
			return nil, fmt.Errorf("expand model path: %w", err)
		}
	}

	modelsDir, err := expandPath(cfg.ModelsDir)
	if err != nil {
		return nil, fmt.Errorf("expand models dir: %w", err)
	}
	cfg.ModelsDir = modelsDir

	if cfg.OrtLibDir != "" {
		cfg.OrtLibDir, err = expandPath(cfg.OrtLibDir)
		if err != nil {
			return nil, fmt.Errorf("expand ort lib dir: %w", err)
		}
	}

	if cfg.XLAPluginDir != "" {
		cfg.XLAPluginDir, err = expandPath(cfg.XLAPluginDir)
		if err != nil {
			return nil, fmt.Errorf("expand xla plugin dir: %w", err)
		}
	}

	content, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("read input file %s: %w", inputPath, err)
	}

	chunks := chunkMarkdown(string(content))
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks extracted from %s", inputPath)
	}

	sessionStart := time.Now()
	session, err := createSession(cfg)
	if err != nil {
		return nil, fmt.Errorf("create %s backend session: %w", cfg.Backend, err)
	}
	sessionElapsed := time.Since(sessionStart)

	if modelPath == "" {
		downloadStart := time.Now()
		modelPath, err = downloadModel(cfg.Model, modelsDir, cfg.Verbose)
		if err != nil {
			session.Destroy() //nolint:errcheck
			return nil, fmt.Errorf("download model %s: %w", cfg.Model, err)
		}
		if cfg.Verbose {
			fmt.Fprintf(os.Stderr, "downloaded model to %s in %s\n", modelPath, time.Since(downloadStart).Round(time.Millisecond))
		}
	}

	pipeStart := time.Now()
	pipeline, err := newPipeline(session, modelPath, cfg.OnnxFilename, cfg.Normalize)
	if err != nil {
		session.Destroy() //nolint:errcheck
		return nil, fmt.Errorf("create feature extraction pipeline: %w", err)
	}
	pipeElapsed := time.Since(pipeStart)

	chunkTexts := make([]string, len(chunks))
	for i, c := range chunks {
		chunkTexts[i] = c.Text
	}

	embedStart := time.Now()
	chunkOutput, err := pipeline.RunPipeline(chunkTexts)
	if err != nil {
		session.Destroy() //nolint:errcheck
		return nil, fmt.Errorf("embed chunks: %w", err)
	}
	chunkEmbedElapsed := time.Since(embedStart)

	return &setupResult{
		InputPath:         inputPath,
		ModelPath:         modelPath,
		Chunks:            chunks,
		ChunkEmbeddings:   chunkOutput.Embeddings,
		Pipeline:          pipeline,
		Session:           session,
		SessionElapsed:    sessionElapsed,
		PipelineElapsed:   pipeElapsed,
		ChunkEmbedElapsed: chunkEmbedElapsed,
	}, nil
}

func createSession(cfg config) (*hugot.Session, error) {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	switch backend {
	case "go":
		return hugot.NewGoSession()
	case "ort":
		var opts []options.WithOption
		if cfg.OrtLibDir != "" {
			opts = append(opts, options.WithOnnxLibraryPath(cfg.OrtLibDir))
		}
		return hugot.NewORTSession(opts...)
	case "xla":
		if cfg.XLAPluginDir != "" {
			if err := os.Setenv("PJRT_PLUGIN_LIBRARY_PATH", cfg.XLAPluginDir); err != nil {
				return nil, fmt.Errorf("set PJRT_PLUGIN_LIBRARY_PATH: %w", err)
			}
		}
		return hugot.NewXLASession()
	default:
		return nil, fmt.Errorf("unsupported backend %q (want go, ort, xla)", cfg.Backend)
	}
}

func downloadModel(model, modelsDir string, verbose bool) (string, error) {
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", fmt.Errorf("create models dir: %w", err)
	}
	opts := hugot.NewDownloadOptions()
	opts.Verbose = verbose
	return hugot.DownloadModel(model, modelsDir, opts)
}

func newPipeline(session *hugot.Session, modelPath, onnxFilename string, normalize bool) (*pipelines.FeatureExtractionPipeline, error) {
	config := hugot.FeatureExtractionConfig{
		ModelPath:    modelPath,
		OnnxFilename: onnxFilename,
		Name:         "embtest",
	}
	if normalize {
		config.Options = []hugot.FeatureExtractionOption{
			pipelines.WithNormalization(),
		}
	}
	return hugot.NewPipeline(session, config)
}

func runBenchmark(cfg config, setup *setupResult) (*benchmarkResult, error) {
	if cfg.BenchIters <= 0 {
		return nil, fmt.Errorf("bench-iters must be > 0")
	}
	if cfg.BenchWarmup < 0 {
		return nil, fmt.Errorf("bench-warmup must be >= 0")
	}

	queryPool := buildBenchmarkQueries(setup.Chunks)
	if len(queryPool) == 0 {
		return nil, fmt.Errorf("failed to derive benchmark queries from %s", setup.InputPath)
	}

	rng := rand.New(rand.NewSource(cfg.BenchSeed))

	for i := 0; i < cfg.BenchWarmup; i++ {
		query := queryPool[rng.Intn(len(queryPool))]
		queryOutput, err := setup.Pipeline.RunPipeline([]string{query})
		if err != nil {
			return nil, fmt.Errorf("warmup query embedding failed: %w", err)
		}
		if len(queryOutput.Embeddings) != 1 {
			return nil, fmt.Errorf("warmup expected one query embedding, got %d", len(queryOutput.Embeddings))
		}
		_ = rankChunks(setup.Chunks, setup.ChunkEmbeddings, queryOutput.Embeddings[0])
	}

	result := &benchmarkResult{
		Queries:            queryPool,
		WarmupIterations:   cfg.BenchWarmup,
		Iterations:         cfg.BenchIters,
		QueryEmbedSamples:  make([]time.Duration, 0, cfg.BenchIters),
		SearchTotalSamples: make([]time.Duration, 0, cfg.BenchIters),
		BestScoreSamples:   make([]float64, 0, cfg.BenchIters),
	}

	benchStart := time.Now()
	for i := 0; i < cfg.BenchIters; i++ {
		query := queryPool[rng.Intn(len(queryPool))]

		totalStart := time.Now()
		queryStart := time.Now()
		queryOutput, err := setup.Pipeline.RunPipeline([]string{query})
		if err != nil {
			return nil, fmt.Errorf("bench query embedding failed on iteration %d: %w", i+1, err)
		}
		queryElapsed := time.Since(queryStart)

		if len(queryOutput.Embeddings) != 1 {
			return nil, fmt.Errorf("bench expected one query embedding on iteration %d, got %d", i+1, len(queryOutput.Embeddings))
		}

		results := rankChunks(setup.Chunks, setup.ChunkEmbeddings, queryOutput.Embeddings[0])
		totalElapsed := time.Since(totalStart)

		bestScore := 0.0
		if len(results) > 0 {
			bestScore = results[0].Score
		}

		result.QueryEmbedSamples = append(result.QueryEmbedSamples, queryElapsed)
		result.SearchTotalSamples = append(result.SearchTotalSamples, totalElapsed)
		result.BestScoreSamples = append(result.BestScoreSamples, bestScore)
	}
	result.TotalBenchmarkTime = time.Since(benchStart)

	return result, nil
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Clean(path), nil
}

func chunkMarkdown(content string) []chunk {
	sections := strings.Split(content, "\n\n")
	chunks := make([]chunk, 0, len(sections))
	var current strings.Builder
	currentIndex := 1
	lastHeading := ""

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text == "" {
			return
		}
		chunks = append(chunks, chunk{
			Index: currentIndex,
			Text:  text,
		})
		currentIndex++
		current.Reset()
	}

	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		if isHeading(section) {
			lastHeading = section
			continue
		}

		block := section
		if lastHeading != "" && !strings.HasPrefix(block, "#") {
			block = lastHeading + "\n\n" + block
		}

		if current.Len() > 0 && current.Len()+len(block)+2 > 1200 {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(block)
	}

	flush()
	return chunks
}

func isHeading(section string) bool {
	lines := strings.Split(section, "\n")
	if len(lines) != 1 {
		return false
	}
	line := strings.TrimSpace(lines[0])
	return strings.HasPrefix(line, "#")
}

func rankChunks(chunks []chunk, embeddings [][]float32, query []float32) []scoredChunk {
	results := make([]scoredChunk, 0, len(chunks))
	for i, embedding := range embeddings {
		if i >= len(chunks) {
			break
		}
		if len(embedding) == 0 {
			continue
		}
		score := cosineSimilarity(query, embedding)
		results = append(results, scoredChunk{
			Chunk:      chunks[i],
			Score:      score,
			Dimensions: len(embedding),
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Chunk.Index < results[j].Chunk.Index
		}
		return results[i].Score > results[j].Score
	})

	return results
}

func buildBenchmarkQueries(chunks []chunk) []string {
	seen := make(map[string]struct{})
	queries := make([]string, 0, len(chunks)*4)

	add := func(text string) {
		text = normalizeSpaces(text)
		if text == "" {
			return
		}
		if len(strings.Fields(text)) < 3 {
			return
		}
		if _, ok := seen[text]; ok {
			return
		}
		seen[text] = struct{}{}
		queries = append(queries, text)
	}

	for _, c := range chunks {
		for _, sentence := range splitIntoSentences(c.Text) {
			add(limitWords(sentence, 16))
		}
		add(limitWords(c.Text, 12))
	}

	return queries
}

func splitIntoSentences(text string) []string {
	replacer := strings.NewReplacer("\r", "\n", "!", ".", "?", ".", ";", ".", "\n", ".")
	text = replacer.Replace(text)
	parts := strings.Split(text, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeSpaces(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func normalizeSpaces(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func limitWords(text string, maxWords int) string {
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:maxWords], " ")
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := min(len(a), len(b))
	var dot float64
	var normA float64
	var normB float64
	for i := 0; i < n; i++ {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		normA += af * af
		normB += bf * bf
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func indentSnippet(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n   ")
}

func printHeader(cfg config, setup *setupResult) {
	fmt.Printf("embtest\n")
	fmt.Printf("backend:      %s\n", strings.ToLower(cfg.Backend))
	fmt.Printf("model:        %s\n", cfg.Model)
	fmt.Printf("model path:   %s\n", setup.ModelPath)
	fmt.Printf("input file:   %s\n", setup.InputPath)
	fmt.Printf("chunks:       %d\n", len(setup.Chunks))
	if cfg.OrtLibDir != "" {
		fmt.Printf("ort lib dir:  %s\n", cfg.OrtLibDir)
	}
	if cfg.XLAPluginDir != "" {
		fmt.Printf("xla plugin:   %s\n", cfg.XLAPluginDir)
	}
	fmt.Printf("normalize:    %v\n", cfg.Normalize)
	fmt.Printf("session init: %s\n", setup.SessionElapsed.Round(time.Millisecond))
	fmt.Printf("pipeline init:%s\n", setup.PipelineElapsed.Round(time.Millisecond))
	fmt.Printf("chunk embed:  %s\n", setup.ChunkEmbedElapsed.Round(time.Millisecond))
}

func printBenchmark(cfg config, setup *setupResult, bench *benchmarkResult) {
	printHeader(cfg, setup)
	fmt.Printf("mode:         bench\n")
	fmt.Printf("query pool:   %d\n", len(bench.Queries))
	fmt.Printf("warmup:       %d\n", bench.WarmupIterations)
	fmt.Printf("iterations:   %d\n", bench.Iterations)
	fmt.Printf("seed:         %d\n", cfg.BenchSeed)
	fmt.Printf("bench total:  %s\n", bench.TotalBenchmarkTime.Round(time.Millisecond))
	fmt.Println()
	fmt.Println("query_embed_ms")
	printDurationStats(bench.QueryEmbedSamples)
	fmt.Println()
	fmt.Println("search_total_ms")
	printDurationStats(bench.SearchTotalSamples)
	fmt.Println()
	fmt.Printf("best_score mean=%.4f median=%.4f min=%.4f max=%.4f\n",
		meanFloat64(bench.BestScoreSamples),
		percentileFloat64(bench.BestScoreSamples, 50),
		minFloat64(bench.BestScoreSamples),
		maxFloat64(bench.BestScoreSamples),
	)
	fmt.Printf("queries_per_sec %.2f\n", float64(bench.Iterations)/bench.TotalBenchmarkTime.Seconds())
}

func printDurationStats(samples []time.Duration) {
	fmt.Printf("count=%d min=%.3f mean=%.3f median=%.3f p95=%.3f p99=%.3f max=%.3f stddev=%.3f\n",
		len(samples),
		durationToMs(minDuration(samples)),
		durationToMs(meanDuration(samples)),
		durationToMs(percentileDuration(samples, 50)),
		durationToMs(percentileDuration(samples, 95)),
		durationToMs(percentileDuration(samples, 99)),
		durationToMs(maxDuration(samples)),
		stddevDurationMs(samples),
	)
}

func durationToMs(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func meanDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	var total time.Duration
	for _, sample := range samples {
		total += sample
	}
	return total / time.Duration(len(samples))
}

func percentileDuration(samples []time.Duration, percentile int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	idx := (len(sorted) * percentile) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func minDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	minVal := samples[0]
	for _, sample := range samples[1:] {
		if sample < minVal {
			minVal = sample
		}
	}
	return minVal
}

func maxDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	maxVal := samples[0]
	for _, sample := range samples[1:] {
		if sample > maxVal {
			maxVal = sample
		}
	}
	return maxVal
}

func stddevDurationMs(samples []time.Duration) float64 {
	if len(samples) == 0 {
		return 0
	}
	mean := durationToMs(meanDuration(samples))
	var sumSquares float64
	for _, sample := range samples {
		diff := durationToMs(sample) - mean
		sumSquares += diff * diff
	}
	variance := sumSquares / float64(len(samples))
	return math.Sqrt(variance)
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func percentileFloat64(values []float64, percentile int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	idx := (len(sorted) * percentile) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func minFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minVal := values[0]
	for _, value := range values[1:] {
		if value < minVal {
			minVal = value
		}
	}
	return minVal
}

func maxFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	maxVal := values[0]
	for _, value := range values[1:] {
		if value > maxVal {
			maxVal = value
		}
	}
	return maxVal
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "embtest: "+format+"\n", args...)
	os.Exit(1)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
