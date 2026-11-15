package retrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type vectorBlob struct {
	Version    int       `json:"version"`
	Profile    string    `json:"profile"`
	Input      string    `json:"input"`
	Checksum   string    `json:"checksum"`
	Dimensions int       `json:"dimensions"`
	Vector     []float32 `json:"vector"`
}

func vectorChecksum(vector []float32) (string, error) {
	data, err := json.Marshal(vector)
	if err != nil {
		return "", err
	}
	return digest(string(data)), nil
}

func (e *Engine) vectorPath(input string) string {
	return filepath.Join(e.root, e.profileID, "vectors", digest(input)+".json")
}

func (e *Engine) readVector(input string) ([]float32, bool, error) {
	if !e.persistent {
		return nil, false, nil
	}
	data, err := os.ReadFile(e.vectorPath(input))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read embedding cache: %w", err)
	}
	var blob vectorBlob
	if err := json.Unmarshal(data, &blob); err != nil {
		return nil, false, fmt.Errorf("decode embedding cache: %w", err)
	}
	checksum, err := vectorChecksum(blob.Vector)
	if err != nil {
		return nil, false, fmt.Errorf("verify cached vector: %w", err)
	}
	if blob.Version != 1 || blob.Profile != e.profileID || blob.Input != input ||
		blob.Dimensions != e.profile.Dimensions || len(blob.Vector) != blob.Dimensions || blob.Checksum != checksum {
		return nil, false, fmt.Errorf("embedding cache identity/dimension/checksum mismatch")
	}
	if _, err := vectorNorm(blob.Vector); err != nil {
		return nil, false, fmt.Errorf("invalid cached vector: %w", err)
	}
	return blob.Vector, true, nil
}

func (e *Engine) writeVector(input string, vector []float32) error {
	if !e.persistent {
		return nil
	}
	checksum, err := vectorChecksum(vector)
	if err != nil {
		return err
	}
	return atomicJSON(e.vectorPath(input), vectorBlob{Version: 1, Profile: e.profileID, Input: input,
		Dimensions: e.profile.Dimensions, Vector: vector, Checksum: checksum})
}

type manifest struct {
	Version    int     `json:"version"`
	Profile    string  `json:"profile"`
	SnapshotID string  `json:"snapshotID"`
	Chunks     []Chunk `json:"chunks"`
}

func (e *Engine) saveManifest(snapshotID string, chunks []Chunk) error {
	if !e.persistent {
		return nil
	}
	path := filepath.Join(e.root, e.profileID, "snapshots", digest(snapshotID)+".json")
	expected := manifest{Version: 1, Profile: e.profileID, SnapshotID: snapshotID, Chunks: chunks}
	data, err := os.ReadFile(path)
	if err == nil {
		var retained manifest
		if err := json.Unmarshal(data, &retained); err != nil {
			return fmt.Errorf("decode retrieval manifest: %w", err)
		}
		want, err := json.Marshal(expected)
		if err != nil {
			return err
		}
		got, err := json.Marshal(retained)
		if err != nil {
			return err
		}
		if string(want) != string(got) {
			return fmt.Errorf("retrieval manifest differs from immutable snapshot")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read retrieval manifest: %w", err)
	}
	return atomicJSON(path, expected)
}

// Publication is atomic; a failed write never replaces a complete cache object.
func atomicJSON(path string, value any) (result error) {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode retrieval cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create retrieval cache directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return fmt.Errorf("create retrieval cache file: %w", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove temporary retrieval cache: %w", err))
		}
	}()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write retrieval cache: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("publish retrieval cache: %w", err)
	}
	return nil
}
