package storage_test

import (
	"context"
	"path"
	"testing"

	"github.com/micvbang/go-helpy/inty"
	"github.com/micvbang/simple-event-broker/internal/infrastructure/logger"
	"github.com/micvbang/simple-event-broker/internal/recordbatch"
	"github.com/micvbang/simple-event-broker/internal/storage"
	"github.com/micvbang/simple-event-broker/internal/tester"
	"github.com/stretchr/testify/require"
)

var (
	log              = logger.NewDefault(context.Background())
	storageFactories = map[string]func() storage.BackingStorage{
		"memory": func() storage.BackingStorage { return storage.NewMemoryTopicStorage(log) },
		"disk":   func() storage.BackingStorage { return storage.NewDiskTopicStorage(log) },
	}
)

// TestStorageEmpty verifies that reading from an empty topic returns
// ErrOutOfBounds.
func TestStorageEmpty(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageEmpty(t, storageFactory())
		})
	}
}
func testStorageEmpty(t *testing.T, backingStorage storage.BackingStorage) {
	tempDir := tester.TempDir(t)

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)
	s, err := storage.NewTopicStorage(log, backingStorage, tempDir, "mytopic", cache, nil)
	require.NoError(t, err)

	// Test
	_, err = s.ReadRecord(0)

	// Verify
	require.ErrorIs(t, err, storage.ErrOutOfBounds)
}

// TestStorageWriteRecordBatchSingleBatch verifies that all records from a
// single Record batch can be read back, and that reading out of bounds returns
// ErrOutOfBounds.
func TestStorageWriteRecordBatchSingleBatch(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageWriteRecordBatchSingleBatch(t, storageFactory())
		})
	}
}
func testStorageWriteRecordBatchSingleBatch(t *testing.T, backingStorage storage.BackingStorage) {
	tempDir := tester.TempDir(t)

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)
	s, err := storage.NewTopicStorage(log, backingStorage, tempDir, "mytopic", cache, nil)
	require.NoError(t, err)

	recordBatch := tester.MakeRandomRecordBatch(5)

	// Test
	err = s.AddRecordBatch(recordBatch)
	require.NoError(t, err)

	// Verify
	for recordID, record := range recordBatch {
		got, err := s.ReadRecord(uint64(recordID))
		require.NoError(t, err)
		require.Equal(t, record, got)
	}

	// Out of bounds reads
	_, err = s.ReadRecord(uint64(len(recordBatch)))
	require.ErrorIs(t, err, storage.ErrOutOfBounds)

	_, err = s.ReadRecord(uint64(len(recordBatch) + 5))
	require.ErrorIs(t, err, storage.ErrOutOfBounds)
}

// TestStorageWriteRecordBatchMultipleBatches verifies that multiple
// RecordBatches can be written to the underlying storage and be read back
// again, and that reading beyond the number of existing records yields
// ErrOutOfBounds.
func TestStorageWriteRecordBatchMultipleBatches(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageWriteRecordBatchMultipleBatches(t, storageFactory())
		})
	}
}
func testStorageWriteRecordBatchMultipleBatches(t *testing.T, backingStorage storage.BackingStorage) {
	tempDir := tester.TempDir(t)

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)
	s, err := storage.NewTopicStorage(log, backingStorage, tempDir, "mytopic", cache, nil)
	require.NoError(t, err)

	recordBatch1 := tester.MakeRandomRecordBatch(5)
	recordBatch2 := tester.MakeRandomRecordBatch(3)

	// Test
	err = s.AddRecordBatch(recordBatch1)
	require.NoError(t, err)

	err = s.AddRecordBatch(recordBatch2)
	require.NoError(t, err)

	// Verify
	for recordID, record := range append(recordBatch1, recordBatch2...) {
		got, err := s.ReadRecord(uint64(recordID))
		require.NoError(t, err)
		require.Equal(t, record, got)
	}

	// Out of bounds reads
	_, err = s.ReadRecord(uint64(len(recordBatch1) + len(recordBatch2)))
	require.ErrorIs(t, err, storage.ErrOutOfBounds)
}

// TestStorageOpenExistingStorage verifies that storage.Storage correctly
// initializes from a topic that already exists and has many data files.
func TestStorageOpenExistingStorage(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageOpenExistingStorage(t, storageFactory())
		})
	}
}
func testStorageOpenExistingStorage(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "my_topic"

	tempDir := tester.TempDir(t)

	totalRecords := 0
	recordBatches := make([]recordbatch.RecordBatch, 50)
	for i := 0; i < len(recordBatches); i++ {
		batchSize := 1 + inty.RandomN(5)
		totalRecords += batchSize
		recordBatches[i] = tester.MakeRandomRecordBatch(batchSize)
	}

	{
		cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
		require.NoError(t, err)
		s1, err := storage.NewTopicStorage(log, backingStorage, tempDir, topicName, cache, nil)
		require.NoError(t, err)

		for _, recordBatch := range recordBatches {
			err = s1.AddRecordBatch(recordBatch)
			require.NoError(t, err)
		}
	}

	// Test
	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)
	s2, err := storage.NewTopicStorage(log, backingStorage, tempDir, topicName, cache, nil)
	require.NoError(t, err)

	// Verify
	recordID := 0
	for _, recordBatch := range recordBatches {
		for _, expected := range recordBatch {
			got, err := s2.ReadRecord(uint64(recordID))
			require.NoError(t, err)
			require.Equal(t, expected, got)

			recordID += 1
		}
	}

	// Out of bounds reads
	_, err = s2.ReadRecord(uint64(totalRecords + 1))
	require.ErrorIs(t, err, storage.ErrOutOfBounds)
}

// TestStorageOpenExistingStorage verifies that storage.Storage correctly
// initializes from a topic that already exists, and can correctly append
// records to it.
// NOTE: this is a regression test that handles an off by one error in
// NewTopicStorage().
func TestStorageOpenExistingStorageAndAppend(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageOpenExistingStorageAndAppend(t, storageFactory())
		})
	}
}
func testStorageOpenExistingStorageAndAppend(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "my_topic"

	tempDir := tester.TempDir(t)

	recordBatch1 := tester.MakeRandomRecordBatch(1)
	{
		cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
		require.NoError(t, err)
		s1, err := storage.NewTopicStorage(log, backingStorage, tempDir, topicName, cache, nil)
		require.NoError(t, err)

		err = s1.AddRecordBatch(recordBatch1)
		require.NoError(t, err)
	}

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)
	s2, err := storage.NewTopicStorage(log, backingStorage, tempDir, topicName, cache, nil)
	require.NoError(t, err)

	// Test
	recordBatch2 := tester.MakeRandomRecordBatch(1)
	err = s2.AddRecordBatch(recordBatch2)
	require.NoError(t, err)

	// Verify
	recordID := 0
	allRecords := append(recordBatch1, recordBatch2...)
	for _, record := range allRecords {
		got, err := s2.ReadRecord(uint64(recordID))
		require.NoError(t, err)
		require.Equal(t, record, got)

		recordID += 1
	}

	// Out of bounds reads
	_, err = s2.ReadRecord(uint64(len(allRecords)))
	require.ErrorIs(t, err, storage.ErrOutOfBounds)
}

// TestStorageCacheWrite verifies that AddRecordBatch uses the cache to cache
// the record batch.
func TestStorageCacheWrite(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageCacheWrite(t, storageFactory())
		})
	}
}
func testStorageCacheWrite(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "my_topic"

	storageDir := tester.TempDir(t)

	cacheStorage := storage.NewMemoryCache(log)
	cache, err := storage.NewCacheDefault(log, cacheStorage)
	require.NoError(t, err)

	s, err := storage.NewTopicStorage(log, backingStorage, storageDir, topicName, cache, nil)
	require.NoError(t, err)

	expectedStorageDir := getStorageKey(storageDir, topicName, 0)
	expectedRecordBatch := tester.MakeRandomRecordBatch(5)

	// Act
	err = s.AddRecordBatch(expectedRecordBatch)
	require.NoError(t, err)

	// Assert

	// record batch must be written to both backing storage and cache.
	_, err = cache.Reader(expectedStorageDir)
	require.NoError(t, err)

	_, err = backingStorage.Reader(expectedStorageDir)
	require.NoError(t, err)

	for recordID, expected := range expectedRecordBatch {
		got, err := s.ReadRecord(uint64(recordID))
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
}

// TestStorageCacheWrite verifies that ReadRecord uses the cache to read
// results.
func TestStorageCacheReadFromCache(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageCacheReadFromCache(t, storageFactory())
		})
	}
}
func testStorageCacheReadFromCache(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "my_topic"

	storageDir := tester.TempDir(t)

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)

	s, err := storage.NewTopicStorage(log, backingStorage, storageDir, topicName, cache, nil)
	require.NoError(t, err)

	expectedRecordBatch := tester.MakeRandomRecordBatch(5)
	err = s.AddRecordBatch(expectedRecordBatch)
	require.NoError(t, err)

	// NOTE: in order to prove that we're reading from the cache and not from the
	// backing storage, we're making the file in the backing storage zero bytes long.
	wtr, err := backingStorage.Writer(getStorageKey(storageDir, topicName, 0))
	require.NoError(t, err)
	tester.WriteAndClose(t, wtr, []byte{})

	for recordID, expected := range expectedRecordBatch {
		// Act
		got, err := s.ReadRecord(uint64(recordID))

		// Assert
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
}

// TestStorageCacheReadFileNotInCache verifies that ReadRecord can fetch record
// batches from the backing storage if it's not in the cache.
func TestStorageCacheReadFileNotInCache(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageCacheReadFileNotInCache(t, storageFactory())
		})
	}
}
func testStorageCacheReadFileNotInCache(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "my_topic"

	storageDir := tester.TempDir(t)

	cacheStorage := storage.NewMemoryCache(log)
	cache, err := storage.NewCacheDefault(log, cacheStorage)
	require.NoError(t, err)

	s, err := storage.NewTopicStorage(log, backingStorage, storageDir, topicName, cache, nil)
	require.NoError(t, err)

	expectedRecordBatch := tester.MakeRandomRecordBatch(5)
	err = s.AddRecordBatch(expectedRecordBatch)
	require.NoError(t, err)

	// NOTE: in order to prove that we're reading from the backing storage and
	// not from the cache, we're removing the file from the cache.
	err = cacheStorage.Remove(getStorageKey(storageDir, topicName, 0))
	require.NoError(t, err)

	for recordID, expected := range expectedRecordBatch {
		// Act
		got, err := s.ReadRecord(uint64(recordID))

		// Assert
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
}

func TestStorageCompressFiles(t *testing.T) {
	for name, storageFactory := range storageFactories {
		t.Run(name, func(t *testing.T) {
			testStorageCompressFiles(t, storageFactory())
		})
	}
}
func testStorageCompressFiles(t *testing.T, backingStorage storage.BackingStorage) {
	const topicName = "topicName"
	storageDir := tester.TempDir(t)

	cache, err := storage.NewCacheDefault(log, storage.NewMemoryCache(log))
	require.NoError(t, err)

	compressor := storage.Gzip{}
	s, err := storage.NewTopicStorage(log, backingStorage, storageDir, topicName, cache, compressor)
	require.NoError(t, err)

	expectedRecordBatch := tester.MakeRandomRecordBatch(5)
	err = s.AddRecordBatch(expectedRecordBatch)
	require.NoError(t, err)

	backingStorageReader, err := backingStorage.Reader(getStorageKey(storageDir, topicName, 0))
	require.NoError(t, err)

	// read records directly from compressor in order to prove that they're compressed
	compressorReader, err := compressor.NewReader(backingStorageReader)
	require.NoError(t, err)

	buf := tester.ReadToMemory(t, compressorReader)

	parser, err := recordbatch.Parse(buf)
	require.NoError(t, err)
	require.Equal(t, uint32(len(expectedRecordBatch)), parser.Header.NumRecords)

	// can read records from compressed data
	for recordID, expected := range expectedRecordBatch {
		// Act
		got, err := s.ReadRecord(uint64(recordID))

		// Assert
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
}

func getStorageKey(storageDir string, topicName string, recordID uint64) string {
	return path.Join(storageDir, storage.RecordBatchPath(topicName, recordID))
}
