package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBaseURL     = "http://127.0.0.1:8080"
	defaultFixturePath = "testdata/stress/fixtures.json"
	defaultTimeout     = 30 * time.Second
	defaultScenario    = "smoke"
	defaultUserAgent   = "gogoclaw-stresstest/1.0"
	defaultListLimit   = 25
	defaultHotConvos   = 8
	defaultUploadEvery = 5
	defaultHealthEvery = 10
	defaultListEvery   = 7
)

type fixtureFile struct {
	Path        string `json:"path"`
	Field       string `json:"field"`
	ContentType string `json:"content_type,omitempty"`
}

type fixture struct {
	Name   string        `json:"name"`
	Weight int           `json:"weight"`
	Text   string        `json:"text"`
	Files  []fixtureFile `json:"files,omitempty"`
}

type fixtureSet struct {
	Requests []fixture `json:"requests"`
}

type thresholds struct {
	P95MS         float64
	P99MS         float64
	MinThroughput float64
	MaxErrorRate  float64
}

type profile struct {
	Name             string
	Duration         time.Duration
	Concurrency      int
	MaxConcurrency   int
	StepConcurrency  int
	StageDuration    time.Duration
	HotConversations int
	UploadEvery      int
	HealthEvery      int
	ListEvery        int
	Thresholds       thresholds
}

type config struct {
	BaseURL             string
	APIKey              string
	FixturePath         string
	Scenario            string
	Timeout             time.Duration
	ListLimit           int
	RandomSeed          int64
	Warmup              time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration
}

type requestResult struct {
	Name        string
	Method      string
	StatusCode  int
	Duration    time.Duration
	Bytes       int
	Err         error
	CompletedAt time.Time
	FailureKind string
}

type metrics struct {
	mu           sync.Mutex
	latencies    []float64
	total        int64
	errors       int64
	bytes        int64
	counts       map[string]int64
	statuses     map[int]int64
	failureKinds map[string]int64
	start        time.Time
	end          time.Time
}

type summary struct {
	Scenario            string
	Total               int64
	Errors              int64
	ErrorRate           float64
	ActiveThroughputRPS float64
	DrainThroughputRPS  float64
	P50MS               float64
	P95MS               float64
	P99MS               float64
	AvgMS               float64
	Statuses            map[int]int64
	Counts              map[string]int64
	FailureKinds        map[string]int64
	Bytes               int64
	ActiveDuration      time.Duration
	DrainDuration       time.Duration
}

type thresholdFailure struct {
	Reasons []string
}

func (f thresholdFailure) Error() string { return strings.Join(f.Reasons, "; ") }

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	fixtures, err := loadFixtures(cfg.FixturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fixture error: %v\n", err)
		os.Exit(1)
	}

	profile, err := scenarioProfile(cfg.Scenario)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario error: %v\n", err)
		os.Exit(1)
	}

	runner := &runner{
		cfg:      cfg,
		client:   newHTTPClient(cfg),
		fixtures: fixtures,
	}

	if cfg.Scenario == "breakpoint" {
		err = runner.runBreakpoint(profile)
	} else {
		_, err = runner.runScenario(profile, profile.Concurrency)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

type runner struct {
	cfg      config
	client   *http.Client
	fixtures []fixture
}

func newHTTPClient(cfg config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = cfg.MaxIdleConns
	transport.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
	transport.MaxConnsPerHost = cfg.MaxConnsPerHost
	transport.IdleConnTimeout = cfg.IdleConnTimeout
	return &http.Client{
		Timeout:   0,
		Transport: transport,
	}
}

func loadConfig() (config, error) {
	cfg := config{
		BaseURL:             envString("STRESS_BASE_URL", defaultBaseURL),
		APIKey:              envString("STRESS_API_KEY", ""),
		FixturePath:         envString("STRESS_FIXTURE_PATH", defaultFixturePath),
		Scenario:            envString("STRESS_SCENARIO", defaultScenario),
		Timeout:             envDuration("STRESS_TIMEOUT", defaultTimeout),
		ListLimit:           envInt("STRESS_LIST_LIMIT", defaultListLimit),
		RandomSeed:          envInt64("STRESS_RANDOM_SEED", time.Now().UnixNano()),
		Warmup:              envDuration("STRESS_WARMUP", 3*time.Second),
		MaxIdleConns:        envInt("STRESS_MAX_IDLE_CONNS", 128),
		MaxIdleConnsPerHost: envInt("STRESS_MAX_IDLE_CONNS_PER_HOST", 64),
		MaxConnsPerHost:     envInt("STRESS_MAX_CONNS_PER_HOST", 0),
		IdleConnTimeout:     envDuration("STRESS_IDLE_CONN_TIMEOUT", 90*time.Second),
	}

	flag.StringVar(&cfg.BaseURL, "base-url", cfg.BaseURL, "Base URL for the REST API")
	flag.StringVar(&cfg.APIKey, "api-key", cfg.APIKey, "REST API bearer token")
	flag.StringVar(&cfg.FixturePath, "fixtures", cfg.FixturePath, "Path to stress fixtures JSON")
	flag.StringVar(&cfg.Scenario, "scenario", cfg.Scenario, "Scenario to run: smoke|sustained|spike|breakpoint")
	flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "Per-request timeout")
	flag.IntVar(&cfg.ListLimit, "list-limit", cfg.ListLimit, "Conversation list page size")
	flag.Int64Var(&cfg.RandomSeed, "seed", cfg.RandomSeed, "Random seed")
	flag.DurationVar(&cfg.Warmup, "warmup", cfg.Warmup, "Unmeasured warmup duration before each measured run")
	flag.IntVar(&cfg.MaxIdleConns, "max-idle-conns", cfg.MaxIdleConns, "HTTP transport max idle connections")
	flag.IntVar(&cfg.MaxIdleConnsPerHost, "max-idle-conns-per-host", cfg.MaxIdleConnsPerHost, "HTTP transport max idle connections per host")
	flag.IntVar(&cfg.MaxConnsPerHost, "max-conns-per-host", cfg.MaxConnsPerHost, "HTTP transport max connections per host (0 = default)")
	flag.DurationVar(&cfg.IdleConnTimeout, "idle-conn-timeout", cfg.IdleConnTimeout, "HTTP transport idle connection timeout")
	flag.Parse()

	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return config{}, fmt.Errorf("parse base URL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return config{}, fmt.Errorf("base URL must include scheme and host")
	}
	if cfg.ListLimit <= 0 {
		cfg.ListLimit = defaultListLimit
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 128
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = 64
	}
	return cfg, nil
}

func loadFixtures(path string) ([]fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixtures: %w", err)
	}
	var set fixtureSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parse fixtures: %w", err)
	}
	if len(set.Requests) == 0 {
		return nil, errors.New("fixtures must contain at least one request")
	}

	baseDir := filepath.Dir(path)
	for i := range set.Requests {
		if set.Requests[i].Weight <= 0 {
			set.Requests[i].Weight = 1
		}
		for j := range set.Requests[i].Files {
			fp := set.Requests[i].Files[j].Path
			if !filepath.IsAbs(fp) {
				set.Requests[i].Files[j].Path = filepath.Join(baseDir, fp)
			}
			if set.Requests[i].Files[j].Field == "" {
				set.Requests[i].Files[j].Field = "file"
			}
		}
	}
	return set.Requests, nil
}

func scenarioProfile(name string) (profile, error) {
	switch strings.ToLower(name) {
	case "smoke":
		return profile{
			Name:             "smoke",
			Duration:         envDuration("STRESS_SMOKE_DURATION", 15*time.Second),
			Concurrency:      envInt("STRESS_SMOKE_CONCURRENCY", 1),
			HotConversations: envInt("STRESS_HOT_CONVERSATIONS", 2),
			UploadEvery:      envInt("STRESS_UPLOAD_EVERY", 2),
			HealthEvery:      envInt("STRESS_HEALTH_EVERY", 4),
			ListEvery:        envInt("STRESS_LIST_EVERY", 3),
			Thresholds: thresholds{
				P95MS:         envFloat("STRESS_THRESHOLD_P95_MS", 1500),
				P99MS:         envFloat("STRESS_THRESHOLD_P99_MS", 2000),
				MinThroughput: envFloat("STRESS_THRESHOLD_MIN_RPS", 0.5),
				MaxErrorRate:  envFloat("STRESS_THRESHOLD_MAX_ERROR_RATE", 0.00),
			},
		}, nil
	case "sustained":
		return profile{
			Name:             "sustained",
			Duration:         envDuration("STRESS_SUSTAINED_DURATION", 2*time.Minute),
			Concurrency:      envInt("STRESS_SUSTAINED_CONCURRENCY", 6),
			HotConversations: envInt("STRESS_HOT_CONVERSATIONS", defaultHotConvos),
			UploadEvery:      envInt("STRESS_UPLOAD_EVERY", defaultUploadEvery),
			HealthEvery:      envInt("STRESS_HEALTH_EVERY", defaultHealthEvery),
			ListEvery:        envInt("STRESS_LIST_EVERY", defaultListEvery),
			Thresholds: thresholds{
				P95MS:         envFloat("STRESS_THRESHOLD_P95_MS", 2200),
				P99MS:         envFloat("STRESS_THRESHOLD_P99_MS", 3000),
				MinThroughput: envFloat("STRESS_THRESHOLD_MIN_RPS", 2.0),
				MaxErrorRate:  envFloat("STRESS_THRESHOLD_MAX_ERROR_RATE", 0.02),
			},
		}, nil
	case "spike":
		return profile{
			Name:             "spike",
			Duration:         envDuration("STRESS_SPIKE_DURATION", 75*time.Second),
			Concurrency:      envInt("STRESS_SPIKE_CONCURRENCY", 12),
			HotConversations: envInt("STRESS_HOT_CONVERSATIONS", defaultHotConvos),
			UploadEvery:      envInt("STRESS_UPLOAD_EVERY", defaultUploadEvery),
			HealthEvery:      envInt("STRESS_HEALTH_EVERY", defaultHealthEvery),
			ListEvery:        envInt("STRESS_LIST_EVERY", defaultListEvery),
			Thresholds: thresholds{
				P95MS:         envFloat("STRESS_THRESHOLD_P95_MS", 3000),
				P99MS:         envFloat("STRESS_THRESHOLD_P99_MS", 4500),
				MinThroughput: envFloat("STRESS_THRESHOLD_MIN_RPS", 3.5),
				MaxErrorRate:  envFloat("STRESS_THRESHOLD_MAX_ERROR_RATE", 0.03),
			},
		}, nil
	case "breakpoint":
		return profile{
			Name:             "breakpoint",
			Concurrency:      envInt("STRESS_BREAKPOINT_START", 2),
			MaxConcurrency:   envInt("STRESS_BREAKPOINT_MAX", 24),
			StepConcurrency:  envInt("STRESS_BREAKPOINT_STEP", 2),
			StageDuration:    envDuration("STRESS_BREAKPOINT_STAGE_DURATION", 20*time.Second),
			HotConversations: envInt("STRESS_HOT_CONVERSATIONS", defaultHotConvos),
			UploadEvery:      envInt("STRESS_UPLOAD_EVERY", defaultUploadEvery),
			HealthEvery:      envInt("STRESS_HEALTH_EVERY", defaultHealthEvery),
			ListEvery:        envInt("STRESS_LIST_EVERY", defaultListEvery),
			Thresholds: thresholds{
				P95MS:         envFloat("STRESS_THRESHOLD_P95_MS", 3200),
				P99MS:         envFloat("STRESS_THRESHOLD_P99_MS", 5000),
				MinThroughput: envFloat("STRESS_THRESHOLD_MIN_RPS", 4.0),
				MaxErrorRate:  envFloat("STRESS_THRESHOLD_MAX_ERROR_RATE", 0.05),
			},
		}, nil
	default:
		return profile{}, fmt.Errorf("unsupported scenario %q", name)
	}
}

func (r *runner) runBreakpoint(p profile) error {
	if p.StepConcurrency <= 0 {
		p.StepConcurrency = 1
	}
	if p.MaxConcurrency < p.Concurrency {
		p.MaxConcurrency = p.Concurrency
	}

	fmt.Printf("==> scenario=breakpoint base_url=%s fixture_path=%s\n", r.cfg.BaseURL, r.cfg.FixturePath)
	lastGood := 0
	for concurrency := p.Concurrency; concurrency <= p.MaxConcurrency; concurrency += p.StepConcurrency {
		stage := p
		stage.Name = fmt.Sprintf("breakpoint-%02d", concurrency)
		stage.Duration = p.StageDuration
		s, err := r.runScenario(stage, concurrency)
		if failure := evaluateThresholds(stage.Thresholds, s); failure != nil {
			fmt.Printf("BREAKPOINT reached at concurrency=%d\n", concurrency)
			fmt.Printf("last_passing_concurrency=%d\n", lastGood)
			return failure
		}
		if err != nil {
			return err
		}
		lastGood = concurrency
	}

	fmt.Printf("BREAKPOINT not reached up to concurrency=%d\n", p.MaxConcurrency)
	return nil
}

func (r *runner) runScenario(p profile, concurrency int) (summary, error) {
	fmt.Printf("==> scenario=%s base_url=%s concurrency=%d duration=%s\n", p.Name, r.cfg.BaseURL, concurrency, p.Duration)
	if r.cfg.Warmup > 0 {
		fmt.Printf("WARMUP duration=%s\n", r.cfg.Warmup)
		r.runLoadWindow(p, concurrency, r.cfg.Warmup, false)
	}

	s := r.runLoadWindow(p, concurrency, p.Duration, true)
	printSummary(s, p.Thresholds)
	return s, nil
}

func (r *runner) runLoadWindow(p profile, concurrency int, duration time.Duration, measure bool) summary {
	m := &metrics{
		latencies:    make([]float64, 0, 1024),
		counts:       make(map[string]int64),
		statuses:     make(map[int]int64),
		failureKinds: make(map[string]int64),
	}
	if measure {
		m.start = time.Now()
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	results := make(chan requestResult, concurrency*4)
	var workers sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		workerID := i
		workerRNG := rand.New(rand.NewSource(r.cfg.RandomSeed + int64(workerID+1)*7919))
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.workerLoop(ctx, p, workerID, workerRNG, results)
		}()
	}

	go func() {
		workers.Wait()
		close(results)
	}()

	for result := range results {
		if measure {
			m.record(result)
		}
	}

	return m.summary(p.Name, duration)
}

func (r *runner) workerLoop(ctx context.Context, p profile, workerID int, workerRNG *rand.Rand, results chan<- requestResult) {
	convID := fmt.Sprintf("stress-hot-%02d", workerID%max(1, p.HotConversations))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result := r.executeRequest(ctx, p, convID, workerRNG)
		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
	}
}

func (r *runner) executeRequest(ctx context.Context, p profile, convID string, workerRNG *rand.Rand) requestResult {
	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	roll := workerRNG.Intn(endpointWeightTotal(p))
	if roll < endpointWeightHealth(p) {
		return r.doHealth(reqCtx)
	}
	roll -= endpointWeightHealth(p)
	if roll < endpointWeightList(p) {
		return r.doListConversations(reqCtx)
	}
	roll -= endpointWeightList(p)
	if roll < endpointWeightUpload(p) {
		fixture := r.pickFixture(true, workerRNG)
		return r.doMessage(reqCtx, fixture, convID, true)
	}
	fixture := r.pickFixture(false, workerRNG)
	return r.doMessage(reqCtx, fixture, convID, false)
}

func endpointWeightHealth(p profile) int {
	if p.HealthEvery <= 0 {
		return 0
	}
	return 1
}

func endpointWeightList(p profile) int {
	if p.ListEvery <= 0 {
		return 0
	}
	return 1
}

func endpointWeightUpload(p profile) int {
	if p.UploadEvery <= 0 {
		return 0
	}
	weight := max(1, 10/p.UploadEvery)
	return weight
}

func endpointWeightTotal(p profile) int {
	total := 10 + endpointWeightUpload(p) + endpointWeightHealth(p) + endpointWeightList(p)
	return max(1, total)
}

func (r *runner) pickFixture(requireFiles bool, workerRNG *rand.Rand) fixture {
	var candidates []fixture
	total := 0
	for _, f := range r.fixtures {
		if requireFiles && len(f.Files) == 0 {
			continue
		}
		if !requireFiles && len(f.Files) > 0 {
			continue
		}
		candidates = append(candidates, f)
		total += f.Weight
	}
	if len(candidates) == 0 {
		candidates = r.fixtures
		total = 0
		for _, f := range candidates {
			total += f.Weight
		}
	}
	target := workerRNG.Intn(max(1, total))
	running := 0
	for _, f := range candidates {
		running += f.Weight
		if target < running {
			return f
		}
	}
	return candidates[len(candidates)-1]
}

func (r *runner) doMessage(ctx context.Context, f fixture, convID string, multipartMode bool) requestResult {
	start := time.Now()

	var req *http.Request
	var err error
	if multipartMode && len(f.Files) > 0 {
		req, err = r.newMultipartRequest(ctx, f, convID)
	} else {
		req, err = r.newJSONRequest(ctx, f, convID)
	}
	if err != nil {
		return requestResult{Name: "message", Method: http.MethodPost, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "setup"}
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return requestResult{Name: "message", Method: req.Method, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "transport"}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	result := requestResult{
		Name:        "message",
		Method:      req.Method,
		StatusCode:  resp.StatusCode,
		Duration:    time.Since(start),
		Bytes:       len(body),
		CompletedAt: time.Now(),
	}
	if readErr != nil {
		result.Err = readErr
	}
	if resp.StatusCode >= 400 {
		result.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return result
}

func (r *runner) newJSONRequest(ctx context.Context, f fixture, convID string) (*http.Request, error) {
	payload := map[string]string{
		"conversation_id": convID,
		"text":            f.Text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/api/message", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	r.applyHeaders(req)
	return req, nil
}

func (r *runner) newMultipartRequest(ctx context.Context, f fixture, convID string) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("conversation_id", convID); err != nil {
		return nil, err
	}
	if err := writer.WriteField("text", f.Text); err != nil {
		return nil, err
	}
	for _, ff := range f.Files {
		data, err := os.ReadFile(ff.Path)
		if err != nil {
			return nil, fmt.Errorf("read upload fixture %s: %w", ff.Path, err)
		}
		part, err := writer.CreateFormFile(ff.Field, filepath.Base(ff.Path))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/api/message", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	r.applyHeaders(req)
	return req, nil
}

func (r *runner) doHealth(ctx context.Context) requestResult {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.cfg.BaseURL+"/api/health", nil)
	if err != nil {
		return requestResult{Name: "health", Method: http.MethodGet, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "setup"}
	}
	r.applyHeaders(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return requestResult{Name: "health", Method: http.MethodGet, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "transport"}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	result := requestResult{
		Name:        "health",
		Method:      http.MethodGet,
		StatusCode:  resp.StatusCode,
		Duration:    time.Since(start),
		Bytes:       len(body),
		CompletedAt: time.Now(),
	}
	if readErr != nil {
		result.Err = readErr
	}
	if resp.StatusCode >= 400 {
		result.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return result
}

func (r *runner) doListConversations(ctx context.Context) requestResult {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/conversations?limit=%d", r.cfg.BaseURL, r.cfg.ListLimit), nil)
	if err != nil {
		return requestResult{Name: "conversations", Method: http.MethodGet, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "setup"}
	}
	r.applyHeaders(req)
	resp, err := r.client.Do(req)
	if err != nil {
		return requestResult{Name: "conversations", Method: http.MethodGet, Duration: time.Since(start), Err: err, CompletedAt: time.Now(), FailureKind: "transport"}
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	result := requestResult{
		Name:        "conversations",
		Method:      http.MethodGet,
		StatusCode:  resp.StatusCode,
		Duration:    time.Since(start),
		Bytes:       len(body),
		CompletedAt: time.Now(),
	}
	if readErr != nil {
		result.Err = readErr
	}
	if resp.StatusCode >= 400 {
		result.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return result
}

func (r *runner) applyHeaders(req *http.Request) {
	req.Header.Set("User-Agent", defaultUserAgent)
	if r.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey)
	}
}

func (m *metrics) record(result requestResult) {
	atomic.AddInt64(&m.total, 1)
	atomic.AddInt64(&m.bytes, int64(result.Bytes))
	if result.Err != nil {
		atomic.AddInt64(&m.errors, 1)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies = append(m.latencies, float64(result.Duration)/float64(time.Millisecond))
	m.counts[result.Name]++
	if result.StatusCode > 0 {
		m.statuses[result.StatusCode]++
	}
	if result.FailureKind != "" {
		m.failureKinds[result.FailureKind]++
	}
	m.end = result.CompletedAt
}

func (m *metrics) summary(name string, activeDuration time.Duration) summary {
	total := atomic.LoadInt64(&m.total)
	errs := atomic.LoadInt64(&m.errors)
	bytes := atomic.LoadInt64(&m.bytes)
	drainDuration := m.end.Sub(m.start)
	if drainDuration <= 0 {
		drainDuration = activeDuration
	}

	m.mu.Lock()
	latencies := append([]float64(nil), m.latencies...)
	counts := cloneCounts(m.counts)
	statuses := cloneStatusCounts(m.statuses)
	failureKinds := cloneCounts(m.failureKinds)
	m.mu.Unlock()

	sort.Float64s(latencies)
	return summary{
		Scenario:            name,
		Total:               total,
		Errors:              errs,
		ErrorRate:           ratio(errs, total),
		ActiveThroughputRPS: float64(total) / activeDuration.Seconds(),
		DrainThroughputRPS:  float64(total) / drainDuration.Seconds(),
		P50MS:               percentile(latencies, 0.50),
		P95MS:               percentile(latencies, 0.95),
		P99MS:               percentile(latencies, 0.99),
		AvgMS:               average(latencies),
		Statuses:            statuses,
		Counts:              counts,
		FailureKinds:        failureKinds,
		Bytes:               bytes,
		ActiveDuration:      activeDuration,
		DrainDuration:       drainDuration,
	}
}

func evaluateThresholds(t thresholds, s summary) error {
	var failures []string
	if s.P95MS > t.P95MS {
		failures = append(failures, fmt.Sprintf("p95 %.1fms > %.1fms", s.P95MS, t.P95MS))
	}
	if s.P99MS > t.P99MS {
		failures = append(failures, fmt.Sprintf("p99 %.1fms > %.1fms", s.P99MS, t.P99MS))
	}
	if s.ActiveThroughputRPS < t.MinThroughput {
		failures = append(failures, fmt.Sprintf("throughput %.2frps < %.2frps", s.ActiveThroughputRPS, t.MinThroughput))
	}
	if s.ErrorRate > t.MaxErrorRate {
		failures = append(failures, fmt.Sprintf("error_rate %.2f%% > %.2f%%", s.ErrorRate*100, t.MaxErrorRate*100))
	}
	if len(failures) == 0 {
		return nil
	}
	return thresholdFailure{Reasons: failures}
}

func printSummary(s summary, t thresholds) {
	fmt.Printf("RESULT scenario=%s total=%d errors=%d error_rate=%.2f%% active_throughput=%.2frps drain_throughput=%.2frps avg=%.2fms p50=%.2fms p95=%.2fms p99=%.2fms active_duration=%s drain_duration=%s bytes=%d\n",
		s.Scenario, s.Total, s.Errors, s.ErrorRate*100, s.ActiveThroughputRPS, s.DrainThroughputRPS, s.AvgMS, s.P50MS, s.P95MS, s.P99MS, s.ActiveDuration.Round(time.Millisecond), s.DrainDuration.Round(time.Millisecond), s.Bytes)
	fmt.Printf("THRESHOLDS p95<=%.1fms p99<=%.1fms throughput>=%.2frps error_rate<=%.2f%%\n",
		t.P95MS, t.P99MS, t.MinThroughput, t.MaxErrorRate*100)
	fmt.Printf("ENDPOINT_COUNTS %s\n", formatNamedCounts(s.Counts))
	fmt.Printf("STATUS_COUNTS %s\n", formatStatusCounts(s.Statuses))
	fmt.Printf("FAILURE_COUNTS %s\n", formatNamedCounts(s.FailureKinds))
	if evaluateThresholds(t, s) == nil {
		fmt.Println("STATUS PASS")
	} else {
		fmt.Println("STATUS FAIL")
	}
}

func formatNamedCounts(counts map[string]int64) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func formatStatusCounts(counts map[int]int64) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	if p >= 1 {
		return values[len(values)-1]
	}
	idx := int(math.Ceil(float64(len(values))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func ratio(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func cloneCounts(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStatusCounts(in map[int]int64) map[int]int64 {
	out := make(map[int]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
