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
	mu                sync.Mutex
	requests          map[requestKey]*requestMetrics
	uploadBytes       int64
	thumbnailHits     int64
	processingSum     float64
	processingCount   int64
	processingBuckets []int64
	directUploads     map[directUploadKey]int64
}

type directUploadKey struct {
	Mode    string
	Outcome string
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
	return &Metrics{requests: map[requestKey]*requestMetrics{}, processingBuckets: make([]int64, len(durationBuckets)+1), directUploads: map[directUploadKey]int64{}}
}

func (m *Metrics) RecordDirectUpload(mode, outcome string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.directUploads[directUploadKey{Mode: mode, Outcome: outcome}]++
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
