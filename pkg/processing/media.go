package processing

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/saker-ai/filehub/pkg/store"
)

func enrichMetadata(ctx context.Context, asset *store.Asset, data []byte) {
	if asset.Metadata == nil {
		asset.Metadata = store.JSONMap{}
	}
	if strings.HasPrefix(asset.ContentType, "video/") || strings.HasPrefix(asset.ContentType, "audio/") {
		for key, value := range probeMedia(ctx, asset.Filename, data) {
			asset.Metadata[key] = value
		}
		return
	}
	if strings.HasPrefix(asset.ContentType, "model/") || isModelFilename(asset.Filename) {
		for key, value := range extractModelMetadata(asset.Filename, data) {
			asset.Metadata[key] = value
		}
	}
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		SampleRate   string `json:"sample_rate"`
		Channels     int    `json:"channels"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func probeMedia(ctx context.Context, filename string, data []byte) store.JSONMap {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return store.JSONMap{}
	}
	tmp, err := os.CreateTemp("", "filehub-media-*"+safeMediaExt(filename))
	if err != nil {
		return store.JSONMap{}
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return store.JSONMap{}
	}
	if err := tmp.Close(); err != nil {
		return store.JSONMap{}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(
		probeCtx,
		"ffprobe",
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		tmpName,
	).Output()
	if err != nil {
		return store.JSONMap{}
	}
	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return store.JSONMap{}
	}
	meta := store.JSONMap{}
	if duration := parseFloat(parsed.Format.Duration); duration > 0 {
		meta["media.duration"] = duration
	}
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if stream.Width > 0 {
				meta["media.width"] = stream.Width
			}
			if stream.Height > 0 {
				meta["media.height"] = stream.Height
			}
			if stream.CodecName != "" {
				meta["media.codec"] = stream.CodecName
			}
			if fps := parseFrameRate(stream.AvgFrameRate); fps > 0 {
				meta["media.fps"] = fps
			}
		case "audio":
			if stream.CodecName != "" {
				meta["media.codec"] = stream.CodecName
			}
			if sampleRate, err := strconv.Atoi(stream.SampleRate); err == nil && sampleRate > 0 {
				meta["media.sample_rate"] = sampleRate
			}
			if stream.Channels > 0 {
				meta["media.channels"] = stream.Channels
			}
		}
	}
	return meta
}

func extractModelMetadata(filename string, data []byte) store.JSONMap {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".obj":
		return extractOBJMetadata(data)
	case ".gltf":
		return extractGLTFMetadata(data)
	case ".glb":
		if jsonChunk := glbJSONChunk(data); len(jsonChunk) > 0 {
			return extractGLTFMetadata(jsonChunk)
		}
	}
	return store.JSONMap{}
}

func extractOBJMetadata(data []byte) store.JSONMap {
	var vertices, faces int
	materials := map[string]struct{}{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "v "):
			vertices++
		case strings.HasPrefix(line, "f "):
			faces++
		case strings.HasPrefix(line, "usemtl "):
			if name := strings.TrimSpace(strings.TrimPrefix(line, "usemtl ")); name != "" {
				materials[name] = struct{}{}
			}
		}
	}
	return modelMetadata(vertices, faces, len(materials))
}

func extractGLTFMetadata(data []byte) store.JSONMap {
	var doc struct {
		Accessors []struct {
			Count int `json:"count"`
		} `json:"accessors"`
		Materials []json.RawMessage `json:"materials"`
		Meshes    []struct {
			Primitives []struct {
				Attributes map[string]int `json:"attributes"`
				Indices    *int           `json:"indices"`
			} `json:"primitives"`
		} `json:"meshes"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &doc); err != nil {
		return store.JSONMap{}
	}
	var vertices, faces int
	for _, mesh := range doc.Meshes {
		for _, primitive := range mesh.Primitives {
			if idx, ok := primitive.Attributes["POSITION"]; ok && idx >= 0 && idx < len(doc.Accessors) {
				vertices += doc.Accessors[idx].Count
			}
			if primitive.Indices != nil && *primitive.Indices >= 0 && *primitive.Indices < len(doc.Accessors) {
				faces += doc.Accessors[*primitive.Indices].Count / 3
			}
		}
	}
	return modelMetadata(vertices, faces, len(doc.Materials))
}

func glbJSONChunk(data []byte) []byte {
	if len(data) < 20 || string(data[:4]) != "glTF" {
		return nil
	}
	offset := 12
	for offset+8 <= len(data) {
		chunkLen := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		chunkType := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		offset += 8
		if chunkLen < 0 || offset+chunkLen > len(data) {
			return nil
		}
		if chunkType == 0x4E4F534A {
			return data[offset : offset+chunkLen]
		}
		offset += chunkLen
	}
	return nil
}

func modelMetadata(vertices, faces, materials int) store.JSONMap {
	meta := store.JSONMap{}
	if vertices > 0 {
		meta["model.vertices"] = vertices
	}
	if faces > 0 {
		meta["model.faces"] = faces
	}
	if materials > 0 {
		meta["model.materials"] = materials
	}
	return meta
}

func parseFrameRate(raw string) float64 {
	num, den, ok := strings.Cut(raw, "/")
	if !ok {
		return parseFloat(raw)
	}
	n := parseFloat(num)
	d := parseFloat(den)
	if n <= 0 || d <= 0 {
		return 0
	}
	return math.Round((n/d)*1000) / 1000
}

func parseFloat(raw string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return v
}

func isModelFilename(filename string) bool {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".glb", ".gltf", ".obj":
		return true
	default:
		return false
	}
}

func safeMediaExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp4", ".mov", ".m4v", ".webm", ".mp3", ".wav", ".flac", ".ogg", ".m4a":
		return ext
	default:
		return ".bin"
	}
}

func writeTempMedia(data []byte, filename string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "filehub-thumb-*"+safeMediaExt(filename))
	if err != nil {
		return "", func() {}, err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return name, cleanup, nil
}

func runFFmpegFrame(ctx context.Context, input, output string, w, h int) error {
	ffmpegCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scale := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h)
	return exec.CommandContext(
		ffmpegCtx,
		"ffmpeg",
		"-v", "error",
		"-y",
		"-i", input,
		"-frames:v", "1",
		"-vf", scale,
		output,
	).Run()
}
