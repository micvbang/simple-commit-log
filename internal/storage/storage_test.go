package storage_test

import (
	"context"
	"testing"

	"github.com/micvbang/simple-event-broker/internal/infrastructure/logger"
	"github.com/micvbang/simple-event-broker/internal/recordbatch"
	"github.com/micvbang/simple-event-broker/internal/storage"
	"github.com/micvbang/simple-event-broker/internal/tester"
	"github.com/stretchr/testify/require"
)

// TestGetRecordsOffsetAndMaxCount verifies that the expected records are
// returned when requesting different offsets, max records, and soft max byte
// limits.
func TestGetRecordsOffsetAndMaxCount(t *testing.T) {
	const autoCreateTopic = true
	tester.TestStorage(t, autoCreateTopic, func(t *testing.T, s *storage.Storage) {
		const topicName = "topic-name"
		ctx := context.Background()

		const recordSize = 16

		allRecords := make(recordbatch.RecordBatch, 32)
		for i := range len(allRecords) {
			allRecords[i] = tester.RandomBytes(t, recordSize)

			_, err := s.AddRecord(topicName, allRecords[i])
			require.NoError(t, err)
		}

		tests := map[string]struct {
			offset       uint64
			maxRecords   int
			softMaxBytes int
			expected     recordbatch.RecordBatch
			err          error
		}{
			"max records zero":          {offset: 0, maxRecords: 0, expected: allRecords[:0]},
			"0-1":                       {offset: 0, maxRecords: 1, expected: allRecords[:1]},
			"0-4":                       {offset: 0, maxRecords: 5, expected: allRecords[0:5]},
			"1-5":                       {offset: 1, maxRecords: 5, expected: allRecords[1:6]},
			"6-6":                       {offset: 6, maxRecords: 1, expected: allRecords[6:7]},
			"0-100":                     {offset: 0, maxRecords: 100, expected: allRecords[:]},
			"32-100 (out of bounds)":    {offset: 32, maxRecords: 100, expected: allRecords[:0], err: storage.ErrOutOfBounds},
			"soft max bytes 5 records":  {offset: 3, maxRecords: 10, softMaxBytes: recordSize * 5, expected: allRecords[3:8]},
			"soft max bytes 10 records": {offset: 7, maxRecords: 10, softMaxBytes: recordSize * 10, expected: allRecords[7:17]},
			"max records 10":            {offset: 5, maxRecords: 10, softMaxBytes: recordSize * 15, expected: allRecords[5:15]},

			// softMaxBytes is only a soft max; return at least one record, even
			// if that record is larger than the soft max.
			"soft max one byte": {offset: 5, maxRecords: 10, softMaxBytes: 1, expected: allRecords[5:6]},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				// Act
				got, err := s.GetRecords(ctx, topicName, test.offset, test.maxRecords, test.softMaxBytes)
				require.ErrorIs(t, err, test.err)

				// Assert
				require.Equal(t, len(test.expected), len(got))
				require.Equal(t, test.expected, got)
			})
		}
	})
}

// TestStorageAutoCreateTopic verifies that AddRecords returns
// storage.ErrTopicNotFound when autoCreateTopic is false, and automatically
// creates the topic if it is true.
func TestAddRecordAutoCreateTopic(t *testing.T) {
	tester.TestBackingStorageAndCache(t, func(t *testing.T, bs storage.BackingStorage, cache *storage.Cache) {
		const topicName = "topic-name"
		expectedRecord := recordbatch.Record("this is a record")

		tests := map[string]struct {
			autoCreateTopic bool
			err             error
		}{
			"false": {autoCreateTopic: false, err: storage.ErrTopicNotFound},
			"true":  {autoCreateTopic: true, err: nil},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				s := storage.NewWithAutoCreate(log,
					func(log logger.Logger, topicName string) (*storage.Topic, error) {
						return storage.NewTopic(log, bs, topicName, cache, &storage.Gzip{})
					},
					func(l logger.Logger, t *storage.Topic) storage.RecordBatcher {
						return storage.NewNullBatcher(t.AddRecordBatch)
					},
					test.autoCreateTopic,
				)

				// Act
				_, err := s.AddRecord(topicName, expectedRecord)

				// Assert
				require.ErrorIs(t, err, test.err)
			})
		}
	})
}

// TestGetRecordsTopicDoesNotExist verifies that GetRecords returns an empty
// record batch when attempting to read from a topic that does not exist.
func TestGetRecordsTopicDoesNotExist(t *testing.T) {
	tester.TestBackingStorageAndCache(t, func(t *testing.T, bs storage.BackingStorage, cache *storage.Cache) {
		const topicName = "topic-name"
		ctx := context.Background()
		record := tester.RandomBytes(t, 8)

		tests := map[string]struct {
			autoCreateTopic bool
			addErr          error
			getErr          error
		}{
			"false": {autoCreateTopic: false, addErr: storage.ErrTopicNotFound, getErr: storage.ErrTopicNotFound},
			"true":  {autoCreateTopic: true, addErr: nil, getErr: storage.ErrOutOfBounds},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				s := storage.NewWithAutoCreate(log,
					func(log logger.Logger, topicName string) (*storage.Topic, error) {
						return storage.NewTopic(log, bs, topicName, cache, &storage.Gzip{})
					},
					func(l logger.Logger, t *storage.Topic) storage.RecordBatcher {
						return storage.NewNullBatcher(t.AddRecordBatch)
					},
					test.autoCreateTopic,
				)

				// will return an error if autoCreateTopic is false
				_, err := s.AddRecord(topicName, record)
				require.ErrorIs(t, err, test.addErr)

				// Act
				got, err := s.GetRecords(ctx, "does-not-exist", 0, 10, 1024)
				require.ErrorIs(t, err, test.getErr)

				// Assert
				require.Equal(t, recordbatch.RecordBatch{}, got)
			})
		}
	})
}

// TestGetRecordsOffsetOutOfBounds verifies that GetRecords returns
// ErrOutOfBounds when attempting to start from an offset that is too high (does
// not yet exist).
func TestGetRecordsOffsetOutOfBounds(t *testing.T) {
	tester.TestBackingStorageAndCache(t, func(t *testing.T, bs storage.BackingStorage, cache *storage.Cache) {
		const topicName = "topic-name"
		ctx := context.Background()

		s := storage.New(log,
			func(log logger.Logger, topicName string) (*storage.Topic, error) {
				return storage.NewTopic(log, bs, topicName, cache, &storage.Gzip{})
			},
			func(l logger.Logger, t *storage.Topic) storage.RecordBatcher {
				return storage.NewNullBatcher(t.AddRecordBatch)
			},
		)

		// add record so that we know there's _something_ in the topic
		offset, err := s.AddRecord(topicName, tester.RandomBytes(t, 8))
		require.NoError(t, err)

		nonExistingOffset := offset + 5

		// Act
		_, err = s.GetRecords(ctx, "does-not-exist", nonExistingOffset, 10, 1024)

		// Assert
		require.ErrorIs(t, err, storage.ErrOutOfBounds)
	})
}

// TestGetRecordsBulkContextImmediatelyCancelled verifies that GetRecords
// respects that the given context has been called.
func TestGetRecordsBulkContextImmediatelyCancelled(t *testing.T) {
	autoCreateTopic := true
	tester.TestStorage(t, autoCreateTopic, func(t *testing.T, s *storage.Storage) {
		const topicName = "topic-name"

		allRecords := tester.MakeRandomRecordBatch(5)
		for _, record := range allRecords {
			_, err := s.AddRecord(topicName, record)
			require.NoError(t, err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act
		got, err := s.GetRecords(ctx, topicName, 0, 10, 1024)

		// Assert
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, recordbatch.RecordBatch{}, got)
	})
}

// TestCreateTopicHappyPath verifies that CreateTopic creates a topic, and that
// GetRecord() and AddRecord() are only successful once the topic has been
// created.
func TestCreateTopicHappyPath(t *testing.T) {
	const autoCreateTopic = false
	tester.TestStorage(t, autoCreateTopic, func(t *testing.T, s *storage.Storage) {
		const topicName = "topic-name"

		_, err := s.GetRecord(topicName, 0)
		require.ErrorIs(t, err, storage.ErrTopicNotFound)

		_, err = s.AddRecord(topicName, recordbatch.Record("this is a record"))
		require.ErrorIs(t, err, storage.ErrTopicNotFound)

		// Act
		err = s.CreateTopic(topicName)
		require.NoError(t, err)

		// Assert
		_, err = s.GetRecord(topicName, 0)
		require.ErrorIs(t, err, storage.ErrOutOfBounds)

		_, err = s.AddRecord(topicName, recordbatch.Record("this is a record"))
		require.NoError(t, err)
	})
}

// TestCreateTopicAlreadyExistsInStorage verifies that calling CreateTopic on
// different instances of storage.Storage returns ErrTopicAlreadyExists when
// attempting to create a topic that already exists in the backing storage (at
// least one record was added to the topic in its lifetime)
func TestCreateTopicAlreadyExistsInStorage(t *testing.T) {
	tester.TestBackingStorageAndCache(t, func(t *testing.T, bs storage.BackingStorage, cache *storage.Cache) {
		const topicName = "topic-name"

		{
			s1 := storage.NewWithAutoCreate(log,
				func(log logger.Logger, topicName string) (*storage.Topic, error) {
					return storage.NewTopic(log, bs, topicName, cache, &storage.Gzip{})
				},
				func(l logger.Logger, t *storage.Topic) storage.RecordBatcher {
					return storage.NewNullBatcher(t.AddRecordBatch)
				},
				false,
			)

			err := s1.CreateTopic(topicName)
			require.NoError(t, err)

			// NOTE: the test relies on there being a created at least one
			// record in the backing storage, since that's the only (current)
			// way to "create" a topic in the backing storage.
			_, err = s1.AddRecord(topicName, recordbatch.Record("this is a record"))
			require.NoError(t, err)
		}

		{
			s2 := storage.NewWithAutoCreate(log,
				func(log logger.Logger, topicName string) (*storage.Topic, error) {
					return storage.NewTopic(log, bs, topicName, cache, &storage.Gzip{})
				},
				func(l logger.Logger, t *storage.Topic) storage.RecordBatcher {
					return storage.NewNullBatcher(t.AddRecordBatch)
				},
				false,
			)

			// Act
			err := s2.CreateTopic(topicName)

			// Assert

			// we expect Storage to complain that topic alreay exists, because
			// it exists in the backing storage.
			require.ErrorIs(t, err, storage.ErrTopicAlreadyExists)
		}
	})
}

// TestCreateTopicAlreadyExists verifies that calling CreateTopic on the same
// instance of storage.Storage will return ErrTopicAlreadyExists if the topic
// was already created.
func TestCreateTopicAlreadyExists(t *testing.T) {
	const autoCreateTopic = false
	tester.TestStorage(t, autoCreateTopic, func(t *testing.T, s *storage.Storage) {
		const topicName = "topic-name"

		// Act
		err := s.CreateTopic(topicName)
		require.NoError(t, err)

		// Assert
		err = s.CreateTopic(topicName)
		require.ErrorIs(t, err, storage.ErrTopicAlreadyExists)
	})
}