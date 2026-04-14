package memory

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cyoda-platform/cyoda-go/internal/common"
)

type messageEntry struct {
	header   common.MessageHeader
	metaData common.MessageMetaData
}

// copyMessageMetaData returns a deep copy of the metadata maps.
func copyMessageMetaData(m common.MessageMetaData) common.MessageMetaData {
	out := common.MessageMetaData{}
	if m.Values != nil {
		out.Values = make(map[string]any, len(m.Values))
		for k, v := range m.Values {
			out.Values[k] = v
		}
	}
	if m.IndexedValues != nil {
		out.IndexedValues = make(map[string]any, len(m.IndexedValues))
		for k, v := range m.IndexedValues {
			out.IndexedValues[k] = v
		}
	}
	return out
}

type MessageStore struct {
	tenant  common.TenantID
	factory *StoreFactory
}

func (s *MessageStore) Save(_ context.Context, id string, header common.MessageHeader, metaData common.MessageMetaData, payload io.Reader) error {
	f := s.factory

	// Step 1: Write blob to a temp file OUTSIDE the lock.
	tenantDir := filepath.Join(f.blobDir, string(s.tenant))
	if err := os.MkdirAll(tenantDir, 0755); err != nil {
		return fmt.Errorf("failed to create tenant blob dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(tenantDir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp blob file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, payload); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write blob payload: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp blob file: %w", err)
	}

	// Step 2: Atomic rename to final path (POSIX atomic).
	blobPath := filepath.Join(tenantDir, id)
	if err := os.Rename(tmpPath, blobPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename blob file: %w", err)
	}

	// Step 3: Acquire lock ONLY for metadata map insertion.
	f.msgMu.Lock()
	tenantMap := f.msgData[s.tenant]
	if tenantMap == nil {
		tenantMap = make(map[string]*messageEntry)
		f.msgData[s.tenant] = tenantMap
	}
	tenantMap[id] = &messageEntry{
		header:   header,
		metaData: copyMessageMetaData(metaData),
	}
	f.msgMu.Unlock()

	return nil
}

func (s *MessageStore) Get(_ context.Context, id string) (common.MessageHeader, common.MessageMetaData, io.ReadCloser, error) {
	f := s.factory

	// Copy metadata under lock.
	f.msgMu.RLock()
	tenantMap := f.msgData[s.tenant]
	entry, ok := tenantMap[id]
	var header common.MessageHeader
	var metaData common.MessageMetaData
	if ok {
		header = entry.header
		metaData = copyMessageMetaData(entry.metaData)
	}
	f.msgMu.RUnlock()

	if !ok {
		return common.MessageHeader{}, common.MessageMetaData{}, nil, common.ErrNotFound
	}

	blobPath := filepath.Join(f.blobDir, string(s.tenant), id)
	file, err := os.Open(blobPath)
	if err != nil {
		return common.MessageHeader{}, common.MessageMetaData{}, nil, fmt.Errorf("failed to open blob file: %w", err)
	}

	return header, metaData, file, nil
}

func (s *MessageStore) Delete(_ context.Context, id string) error {
	f := s.factory

	// Remove metadata under lock.
	f.msgMu.Lock()
	tenantMap := f.msgData[s.tenant]
	if tenantMap != nil {
		delete(tenantMap, id)
	}
	f.msgMu.Unlock()

	// Remove blob file outside lock (best-effort).
	blobPath := filepath.Join(f.blobDir, string(s.tenant), id)
	os.Remove(blobPath)

	return nil
}

func (s *MessageStore) DeleteBatch(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
