package api

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/saker-ai/filehub/pkg/store"
)

var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type Metrics struct {
	mu                 sync.Mutex
	requests           map[requestKey]*requestMetrics
	uploadBytes        int64
	thumbnailHits      int64
	processingSum      float64
	processingCount    int64
	processingBuckets  []int64
	directUploads      map[directUploadKey]int64
	workspaceCommits   map[string]int64
	workspaceOps       map[workspaceOpKey]int64
	workspaceConflicts int64
	workspaceReads     map[string]int64
	workspaceBytes     map[string]int64
}

type directUploadKey struct {
	Mode    string
	Outcome string
}

type workspaceOpKey struct {
	Kind       string
	Resolution string
}

type requestKey struct {
	Method string
	Path   string
	Status string
}

type requestMetrics struct {
	Count   int64
	Sum     float64
	Buckets []int64
}

func NewMetrics() *Metrics {
	return &Metrics{
		requests:          map[requestKey]*requestMetrics{},
		processingBuckets: make([]int64, len(durationBuckets)+1),
		directUploads:     map[directUploadKey]int64{},
		workspaceCommits:  map[string]int64{},
		workspaceOps:      map[workspaceOpKey]int64{},
		workspaceReads:    map[string]int64{},
		workspaceBytes:    map[string]int64{},
	}
}

func (m *Metrics) RecordDirectUpload(mode, outcome string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.directUploads[directUploadKey{Mode: mode, Outcome: outcome}]++
	m.mu.Unlock()
}

// RecordWorkspaceCommit counts workspace commits by outcome (doc §12).
func (m *Metrics) RecordWorkspaceCommit(outcome string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workspaceCommits[outcome]++
	m.mu.Unlock()
}

// RecordWorkspaceOperation counts committed operations by kind/resolution.
func (m *Metrics) RecordWorkspaceOperation(kind, resolution string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workspaceOps[workspaceOpKey{Kind: kind, Resolution: resolution}]++
	m.mu.Unlock()
}

// RecordWorkspaceConflict counts conflict resolutions.
func (m *Metrics) RecordWorkspaceConflict() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workspaceConflicts++
	m.mu.Unlock()
}

// RecordWorkspaceReadEvent counts read events by kind.
func (m *Metrics) RecordWorkspaceReadEvent(kind string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.workspaceReads[kind]++
	m.mu.Unlock()
}

// AddWorkspaceSyncBytes counts synced bytes by direction ("in" committed to
// FileHub, "out" served via shares).
func (m *Metrics) AddWorkspaceSyncBytes(direction string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	m.workspaceBytes[direction] += n
	m.mu.Unlock()
}

func (m *Metrics) RecordRequest(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		route = "unmatched"
	}
	key := requestKey{Method: method, Path: route, Status: fmt.Sprint(status)}
	seconds := duration.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	rm := m.requests[key]
	if rm == nil {
		rm = &requestMetrics{Buckets: make([]int64, len(durationBuckets)+1)}
		m.requests[key] = rm
	}
	rm.Count++
	rm.Sum += seconds
	for i, bucket := range durationBuckets {
		if seconds <= bucket {
			rm.Buckets[i]++
		}
	}
	rm.Buckets[len(rm.Buckets)-1]++
}

func (m *Metrics) AddUploadBytes(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	m.uploadBytes += n
	m.mu.Unlock()
}

func (m *Metrics) AddThumbnailHit() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.thumbnailHits++
	m.mu.Unlock()
}

func (m *Metrics) ObserveProcessing(duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	seconds := duration.Seconds()
	m.processingSum += seconds
	m.processingCount++
	for i, bucket := range durationBuckets {
		if seconds <= bucket {
			m.processingBuckets[i]++
		}
	}
	m.processingBuckets[len(m.processingBuckets)-1]++
	m.mu.Unlock()
}

func (m *Metrics) Render(stats *store.AssetStats) string {
	if m == nil {
		m = NewMetrics()
	}
	m.mu.Lock()
	keys := make([]requestKey, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path != keys[j].Path {
			return keys[i].Path < keys[j].Path
		}
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].Status < keys[j].Status
	})
	var b strings.Builder
	b.WriteString("# HELP filehub_requests_total HTTP requests by method, route, and status.\n")
	b.WriteString("# TYPE filehub_requests_total counter\n")
	for _, key := range keys {
		rm := m.requests[key]
		fmt.Fprintf(&b, "filehub_requests_total{method=%q,path=%q,status=%q} %d\n", key.Method, key.Path, key.Status, rm.Count)
	}
	b.WriteString("# HELP filehub_request_duration_seconds HTTP request duration histogram.\n")
	b.WriteString("# TYPE filehub_request_duration_seconds histogram\n")
	for _, key := range keys {
		rm := m.requests[key]
		for i, bucket := range durationBuckets {
			fmt.Fprintf(&b, "filehub_request_duration_seconds_bucket{method=%q,path=%q,status=%q,le=%q} %d\n", key.Method, key.Path, key.Status, fmt.Sprintf("%g", bucket), rm.Buckets[i])
		}
		fmt.Fprintf(&b, "filehub_request_duration_seconds_bucket{method=%q,path=%q,status=%q,le=%q} %d\n", key.Method, key.Path, key.Status, "+Inf", rm.Buckets[len(rm.Buckets)-1])
		fmt.Fprintf(&b, "filehub_request_duration_seconds_sum{method=%q,path=%q,status=%q} %g\n", key.Method, key.Path, key.Status, rm.Sum)
		fmt.Fprintf(&b, "filehub_request_duration_seconds_count{method=%q,path=%q,status=%q} %d\n", key.Method, key.Path, key.Status, rm.Count)
	}
	uploadBytes := m.uploadBytes
	thumbnailHits := m.thumbnailHits
	processingSum := m.processingSum
	processingCount := m.processingCount
	processingBuckets := append([]int64(nil), m.processingBuckets...)
	directUploads := make(map[directUploadKey]int64, len(m.directUploads))
	for key, count := range m.directUploads {
		directUploads[key] = count
	}
	workspaceCommits := make(map[string]int64, len(m.workspaceCommits))
	for key, count := range m.workspaceCommits {
		workspaceCommits[key] = count
	}
	workspaceOps := make(map[workspaceOpKey]int64, len(m.workspaceOps))
	for key, count := range m.workspaceOps {
		workspaceOps[key] = count
	}
	workspaceReads := make(map[string]int64, len(m.workspaceReads))
	for key, count := range m.workspaceReads {
		workspaceReads[key] = count
	}
	workspaceBytes := make(map[string]int64, len(m.workspaceBytes))
	for key, count := range m.workspaceBytes {
		workspaceBytes[key] = count
	}
	workspaceConflicts := m.workspaceConflicts
	m.mu.Unlock()

	b.WriteString("# HELP filehub_upload_bytes_total Uploaded bytes accepted by FileHub.\n")
	b.WriteString("# TYPE filehub_upload_bytes_total counter\n")
	fmt.Fprintf(&b, "filehub_upload_bytes_total %d\n", uploadBytes)
	b.WriteString("# HELP filehub_thumbnail_cache_hits_total Thumbnail cache hits.\n")
	b.WriteString("# TYPE filehub_thumbnail_cache_hits_total counter\n")
	fmt.Fprintf(&b, "filehub_thumbnail_cache_hits_total %d\n", thumbnailHits)
	b.WriteString("# HELP filehub_processing_duration_seconds Asset processing duration histogram.\n")
	b.WriteString("# TYPE filehub_processing_duration_seconds histogram\n")
	for i, bucket := range durationBuckets {
		fmt.Fprintf(&b, "filehub_processing_duration_seconds_bucket{le=%q} %d\n", fmt.Sprintf("%g", bucket), processingBuckets[i])
	}
	fmt.Fprintf(&b, "filehub_processing_duration_seconds_bucket{le=%q} %d\n", "+Inf", processingBuckets[len(processingBuckets)-1])
	fmt.Fprintf(&b, "filehub_processing_duration_seconds_sum %g\n", processingSum)
	fmt.Fprintf(&b, "filehub_processing_duration_seconds_count %d\n", processingCount)
	b.WriteString("# HELP filehub_direct_uploads_total Direct upload lifecycle events.\n")
	b.WriteString("# TYPE filehub_direct_uploads_total counter\n")
	directKeys := make([]directUploadKey, 0, len(directUploads))
	for key := range directUploads {
		directKeys = append(directKeys, key)
	}
	sort.Slice(directKeys, func(i, j int) bool {
		if directKeys[i].Mode != directKeys[j].Mode {
			return directKeys[i].Mode < directKeys[j].Mode
		}
		return directKeys[i].Outcome < directKeys[j].Outcome
	})
	for _, key := range directKeys {
		fmt.Fprintf(&b, "filehub_direct_uploads_total{mode=%q,outcome=%q} %d\n", key.Mode, key.Outcome, directUploads[key])
	}
	b.WriteString("# HELP filehub_workspace_commits_total Workspace commits by outcome.\n")
	b.WriteString("# TYPE filehub_workspace_commits_total counter\n")
	for _, key := range sortedKeys(workspaceCommits) {
		fmt.Fprintf(&b, "filehub_workspace_commits_total{outcome=%q} %d\n", key, workspaceCommits[key])
	}
	b.WriteString("# HELP filehub_workspace_operations_total Workspace commit operations by kind and resolution.\n")
	b.WriteString("# TYPE filehub_workspace_operations_total counter\n")
	opKeys := make([]workspaceOpKey, 0, len(workspaceOps))
	for key := range workspaceOps {
		opKeys = append(opKeys, key)
	}
	sort.Slice(opKeys, func(i, j int) bool {
		if opKeys[i].Kind != opKeys[j].Kind {
			return opKeys[i].Kind < opKeys[j].Kind
		}
		return opKeys[i].Resolution < opKeys[j].Resolution
	})
	for _, key := range opKeys {
		fmt.Fprintf(&b, "filehub_workspace_operations_total{kind=%q,resolution=%q} %d\n", key.Kind, key.Resolution, workspaceOps[key])
	}
	b.WriteString("# HELP filehub_workspace_conflicts_total Workspace conflict resolutions.\n")
	b.WriteString("# TYPE filehub_workspace_conflicts_total counter\n")
	fmt.Fprintf(&b, "filehub_workspace_conflicts_total %d\n", workspaceConflicts)
	b.WriteString("# HELP filehub_workspace_read_events_total Workspace read events by kind.\n")
	b.WriteString("# TYPE filehub_workspace_read_events_total counter\n")
	for _, key := range sortedKeys(workspaceReads) {
		fmt.Fprintf(&b, "filehub_workspace_read_events_total{kind=%q} %d\n", key, workspaceReads[key])
	}
	b.WriteString("# HELP filehub_workspace_sync_bytes_total Workspace synced bytes by direction.\n")
	b.WriteString("# TYPE filehub_workspace_sync_bytes_total counter\n")
	for _, key := range sortedKeys(workspaceBytes) {
		fmt.Fprintf(&b, "filehub_workspace_sync_bytes_total{direction=%q} %d\n", key, workspaceBytes[key])
	}
	if stats != nil {
		b.WriteString("# HELP filehub_storage_bytes Current stored bytes.\n")
		b.WriteString("# TYPE filehub_storage_bytes gauge\n")
		fmt.Fprintf(&b, "filehub_storage_bytes %d\n", stats.TotalBytes)
		b.WriteString("# HELP filehub_assets_total Assets by purpose or status.\n")
		b.WriteString("# TYPE filehub_assets_total gauge\n")
		fmt.Fprintf(&b, "filehub_assets_total{purpose=%q,status=%q} %d\n", "all", "all", stats.Total)
		for _, pair := range sortedCounts(stats.ByPurpose) {
			fmt.Fprintf(&b, "filehub_assets_total{purpose=%q,status=%q} %d\n", pair.Key, "all", pair.Count)
		}
		for _, pair := range sortedCounts(stats.ByStatus) {
			fmt.Fprintf(&b, "filehub_assets_total{purpose=%q,status=%q} %d\n", "all", pair.Key, pair.Count)
		}
	}
	return b.String()
}

type countPair struct {
	Key   string
	Count int64
}

func sortedCounts(values map[string]int64) []countPair {
	out := make([]countPair, 0, len(values))
	for key, count := range values {
		out = append(out, countPair{Key: key, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func sortedKeys(values map[string]int64) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
