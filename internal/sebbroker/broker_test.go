package sebbroker_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micvbang/go-helpy/inty"
	"github.com/micvbang/go-helpy/sizey"
	"github.com/micvbang/go-helpy/slicey"
	"github.com/micvbang/go-helpy/timey"
	seb "github.com/micvbang/simple-event-broker"
	"github.com/micvbang/simple-event-broker/internal/infrastructure/logger"
	"github.com/micvbang/simple-event-broker/internal/infrastructure/tester"
	"github.com/micvbang/simple-event-broker/internal/sebbroker"
	"github.com/micvbang/simple-event-broker/internal/sebcache"
	"github.com/micvbang/simple-event-broker/internal/sebrecords"
	"github.com/micvbang/simple-event-broker/internal/sebtopic"
	"github.com/stretchr/testify/require"
)

var (
	log = logger.NewWithLevel(context.Background(), logger.LevelWarn)
)

// TestGetRecordsOffsetAndMaxCount verifies that the expected records are
// returned when requesting different offsets, max records, and soft max byte
// limits.
func TestGetRecordsOffsetAndMaxCount(t *testing.T) {
	const autoCreateTopic = true
	tester.TestBroker(t, autoCreateTopic, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"
		ctx := context.Background()

		const (
			recordSize        = 16
			maxRecordsDefault = 10
		)

		allRecords := make([][]byte, 32)
		for i := range len(allRecords) {
			allRecords[i] = tester.RandomBytes(t, recordSize)

			_, err := s.AddRecord(topicName, allRecords[i])
			require.NoError(t, err)
		}

		tests := map[string]struct {
			offset       uint64
			maxRecords   int
			softMaxBytes int
			expected     [][]byte
			err          error
		}{
			"max records zero":          {offset: 0, maxRecords: 0, expected: allRecords[:maxRecordsDefault]},
			"0-1":                       {offset: 0, maxRecords: 1, expected: allRecords[:1]},
			"0-4":                       {offset: 0, maxRecords: 5, expected: allRecords[0:5]},
			"1-5":                       {offset: 1, maxRecords: 5, expected: allRecords[1:6]},
			"6-6":                       {offset: 6, maxRecords: 1, expected: allRecords[6:7]},
			"0-100":                     {offset: 0, maxRecords: 100, expected: allRecords},
			"32-100 (out of bounds)":    {offset: 32, maxRecords: 100, expected: nil, err: context.DeadlineExceeded},
			"soft max bytes 5 records":  {offset: 3, maxRecords: 10, softMaxBytes: recordSize * 5, expected: allRecords[3:8]},
			"soft max bytes 10 records": {offset: 7, maxRecords: 10, softMaxBytes: recordSize * 10, expected: allRecords[7:17]},
			"max records 10":            {offset: 5, maxRecords: 10, softMaxBytes: recordSize * 15, expected: allRecords[5:15]},

			// softMaxBytes is only a soft max; return at least one record, even
			// if that record is larger than the soft max.
			"soft max one byte": {offset: 5, maxRecords: 10, softMaxBytes: 1, expected: allRecords[5:6]},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()

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

// TestAddRecordsAutoCreateTopic verifies that AddRecord and AddRecords returns
// seb.ErrTopicNotFound when autoCreateTopic is false, and automatically creates
// the topic when it is true.
func TestAddRecordsAutoCreateTopic(t *testing.T) {
	tester.TestTopicStorageAndCache(t, func(t *testing.T, ts sebtopic.Storage, cache *sebcache.Cache) {
		tests := map[string]struct {
			autoCreateTopic bool
			err             error
		}{
			"false": {autoCreateTopic: false, err: seb.ErrTopicNotFound},
			"true":  {autoCreateTopic: true, err: nil},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				broker := sebbroker.New(log,
					sebbroker.NewTopicFactory(ts, cache),
					sebbroker.WithNullBatcher(),
					sebbroker.WithAutoCreateTopic(test.autoCreateTopic),
				)

				// AddRecord
				{
					// Act
					_, err := broker.AddRecord("first", []byte("this is a record"))

					// Assert
					require.ErrorIs(t, err, test.err)
				}

				// AddRecords
				{
					batch := tester.MakeRandomRecordBatch(5)
					// Act
					_, err := broker.AddRecords("second", batch)

					// Assert
					require.ErrorIs(t, err, test.err)
				}
			})
		}
	})
}

// TestGetRecordsTopicDoesNotExist verifies that GetRecords returns an empty
// record batch when attempting to read from a topic that does not exist.
func TestGetRecordsTopicDoesNotExist(t *testing.T) {
	tester.TestTopicStorageAndCache(t, func(t *testing.T, ts sebtopic.Storage, cache *sebcache.Cache) {
		const topicName = "topic-name"
		ctx := context.Background()
		record := tester.RandomBytes(t, 8)

		tests := map[string]struct {
			autoCreateTopic bool
			addErr          error
			getErr          error
		}{
			"false": {autoCreateTopic: false, addErr: seb.ErrTopicNotFound, getErr: seb.ErrTopicNotFound},
			"true":  {autoCreateTopic: true, addErr: nil, getErr: seb.ErrOutOfBounds},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				broker := sebbroker.New(log,
					sebbroker.NewTopicFactory(ts, cache),
					sebbroker.WithNullBatcher(),
					sebbroker.WithAutoCreateTopic(test.autoCreateTopic),
				)

				// will return an error if autoCreateTopic is false
				_, err := broker.AddRecord(topicName, record)
				require.ErrorIs(t, err, test.addErr)

				// Act
				got, err := broker.GetRecords(ctx, "does-not-exist", 0, 10, 1024)
				require.ErrorIs(t, err, test.getErr)

				// Assert
				var expected [][]byte
				require.Equal(t, expected, got)
			})
		}
	})
}

// TestGetRecordsOffsetOutOfBounds verifies that GetRecords returns
// context.DeadlineExceeded when attempting to read an offset that is too high
// (does not yet exist).
func TestGetRecordsOffsetOutOfBounds(t *testing.T) {
	const autoCreateTopic = true
	tester.TestBroker(t, autoCreateTopic, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"

		// add record so that we know there's _something_ in the topic
		offset, err := s.AddRecord(topicName, tester.RandomBytes(t, 8))
		require.NoError(t, err)

		nonExistingOffset := offset + 5

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		// Act
		_, err = s.GetRecords(ctx, "does-not-exist", nonExistingOffset, 10, 1024)

		// Assert
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestGetRecordsBulkContextImmediatelyCancelled verifies that GetRecords
// respects that the given context has been called.
func TestGetRecordsBulkContextImmediatelyCancelled(t *testing.T) {
	autoCreateTopic := true
	tester.TestBroker(t, autoCreateTopic, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"

		batch := tester.MakeRandomRecordBatch(5)
		_, err := s.AddRecords(topicName, batch)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Act
		got, err := s.GetRecords(ctx, topicName, 0, 10, 1024)

		// Assert
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, [][]byte(nil), got)
	})
}

// TestCreateTopicHappyPath verifies that CreateTopic creates a topic, and that
// GetRecord() and AddRecord() are only successful once the topic has been
// created.
func TestCreateTopicHappyPath(t *testing.T) {
	const autoCreateTopic = false
	tester.TestBroker(t, autoCreateTopic, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"

		_, err := s.GetRecord(topicName, 0)
		require.ErrorIs(t, err, seb.ErrTopicNotFound)

		_, err = s.AddRecord(topicName, []byte("this is a record"))
		require.ErrorIs(t, err, seb.ErrTopicNotFound)

		// Act
		err = s.CreateTopic(topicName)
		require.NoError(t, err)

		// Assert
		_, err = s.GetRecord(topicName, 0)
		require.ErrorIs(t, err, seb.ErrOutOfBounds)

		_, err = s.AddRecord(topicName, []byte("this is a record"))
		require.NoError(t, err)

		// ensure that GetRecord does not block waiting for record to become
		// available
		_, err = s.GetRecord(topicName, 2)
		require.ErrorIs(t, err, seb.ErrOutOfBounds)
	})
}

// TestCreateTopicAlreadyExistsInStorage verifies that calling CreateTopic on
// different instances of storage.Storage returns ErrTopicAlreadyExists when
// attempting to create a topic that already exists in topic storage (at least
// one record was added to the topic in its lifetime)
func TestCreateTopicAlreadyExistsInStorage(t *testing.T) {
	tester.TestTopicStorageAndCache(t, func(t *testing.T, bs sebtopic.Storage, cache *sebcache.Cache) {
		const topicName = "topic-name"

		{
			s1 := sebbroker.New(log,
				func(log logger.Logger, topicName string) (*sebtopic.Topic, error) {
					return sebtopic.New(log, bs, topicName, cache)
				},
				sebbroker.WithNullBatcher(),
				sebbroker.WithAutoCreateTopic(false),
			)

			err := s1.CreateTopic(topicName)
			require.NoError(t, err)

			// NOTE: the test relies on there being a created at least one
			// record in topic storage, since that's the only (current) way to
			// persist information about a topic's existence.
			_, err = s1.AddRecord(topicName, []byte("this is a record"))
			require.NoError(t, err)
		}

		{
			s2 := sebbroker.New(log,
				func(log logger.Logger, topicName string) (*sebtopic.Topic, error) {
					return sebtopic.New(log, bs, topicName, cache)
				},
				sebbroker.WithNullBatcher(),
				sebbroker.WithAutoCreateTopic(false),
			)

			// Act
			err := s2.CreateTopic(topicName)

			// Assert
			// we expect Storage to complain that topic alreay exists, because
			// it exists in the backing storage.
			require.ErrorIs(t, err, seb.ErrTopicAlreadyExists)
		}
	})
}

// TestCreateTopicAlreadyExists verifies that calling CreateTopic on the same
// instance of storage.Storage will return ErrTopicAlreadyExists if the topic
// was already created.
func TestCreateTopicAlreadyExists(t *testing.T) {
	const autoCreateTopic = false
	tester.TestBroker(t, autoCreateTopic, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"

		// Act
		err := s.CreateTopic(topicName)
		require.NoError(t, err)

		// Assert
		err = s.CreateTopic(topicName)
		require.ErrorIs(t, err, seb.ErrTopicAlreadyExists)
	})
}

// TestBrokerMetadataHappyPath verifies that Metadata() returns the expected
// data for a topic that exists.
func TestBrokerMetadataHappyPath(t *testing.T) {
	const autoCreate = true
	tester.TestBroker(t, autoCreate, func(t *testing.T, s *sebbroker.Broker) {
		const topicName = "topic-name"

		for numRecords := 1; numRecords <= 10; numRecords++ {
			_, err := s.AddRecord(topicName, []byte("this be record"))
			require.NoError(t, err)

			gotMetadata, err := s.Metadata(topicName)
			require.NoError(t, err)
			t0 := time.Now()

			require.Equal(t, uint64(numRecords), gotMetadata.NextOffset)
			require.True(t, timey.DiffEqual(5*time.Millisecond, t0, gotMetadata.LatestCommitAt))
		}
	})
}

// TestBrokerMetadataTopicNotFound verifies that ErrTopicNotFound is returned
// when attempting to read metadata from a topic that does not exist, when topic
// auto creation is turned off.
func TestBrokerMetadataTopicNotFound(t *testing.T) {
	tests := map[string]struct {
		autoCreate  bool
		expectedErr error
	}{
		"no auto create": {autoCreate: false, expectedErr: seb.ErrTopicNotFound},
		"auto create":    {autoCreate: true, expectedErr: nil},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tester.TestBroker(t, test.autoCreate, func(t *testing.T, s *sebbroker.Broker) {
				_, err := s.Metadata("does-not-exist")
				require.ErrorIs(t, err, test.expectedErr)
			})
		})
	}
}

// TestAddRecordsHappyPath verifies that AddRecords adds the expected records.
func TestAddRecordsHappyPath(t *testing.T) {
	tester.TestTopicStorageAndCache(t, func(t *testing.T, ts sebtopic.Storage, cache *sebcache.Cache) {
		broker := sebbroker.New(log,
			sebbroker.NewTopicFactory(ts, cache),
			sebbroker.WithNullBatcher(),
			sebbroker.WithAutoCreateTopic(true),
		)

		const topicName = "topic"
		batch := tester.MakeRandomRecordBatch(5)
		expectedRecords := tester.BatchIndividualRecords(t, batch, 0, batch.Len())

		// Act
		_, err := broker.AddRecords(topicName, batch)
		require.NoError(t, err)

		// Assert
		gotRecords, err := broker.GetRecords(context.Background(), topicName, 0, 9999, 0)
		require.NoError(t, err)

		require.Equal(t, expectedRecords, gotRecords)
	})
}

// TestAddRecordHappyPath verifies that AddRecord adds the expected records.
func TestAddRecordHappyPath(t *testing.T) {
	tester.TestTopicStorageAndCache(t, func(t *testing.T, ts sebtopic.Storage, cache *sebcache.Cache) {
		broker := sebbroker.New(log,
			sebbroker.NewTopicFactory(ts, cache),
			sebbroker.WithNullBatcher(),
			sebbroker.WithAutoCreateTopic(true),
		)

		const topicName = "topic"
		batch := tester.MakeRandomRecordBatch(5)
		expectedRecords := tester.BatchIndividualRecords(t, batch, 0, batch.Len())

		// Act
		for _, record := range expectedRecords {
			_, err := broker.AddRecord(topicName, record)
			require.NoError(t, err)
		}

		// Assert
		gotRecords, err := broker.GetRecords(context.Background(), topicName, 0, 9999, 0)
		require.NoError(t, err)

		require.Equal(t, expectedRecords, gotRecords)
	})
}

// TestBrokerConcurrency exercises thread safety when doing reads and writes
// concurrently.
func TestBrokerConcurrency(t *testing.T) {
	const autoCreate = true
	tester.TestBroker(t, autoCreate, func(t *testing.T, s *sebbroker.Broker) {
		ctx := context.Background()

		batches := make([]sebrecords.Batch, 50)
		for i := 0; i < len(batches); i++ {
			batches[i] = tester.MakeRandomRecordBatchSize(inty.RandomN(32)+1, 64*sizey.B)
		}

		topicNames := []string{
			"topic1",
			"topic2",
			"topic3",
			"topic4",
			"topic5",
		}

		const (
			batchAdders  = 50
			singleAdders = 100
			verifiers    = 10
		)

		type verification struct {
			topicName string
			offset    uint64
			records   [][]byte
		}
		verifications := make(chan verification, (batchAdders+singleAdders)*2)

		var recordsAdded atomic.Int32
		stopWrites := make(chan struct{})

		wg := sync.WaitGroup{}
		wg.Add(batchAdders + singleAdders)

		// concurrently add records using AddRecords()
		for range batchAdders {
			go func() {
				defer wg.Done()

				added := 0
				for {
					select {
					case <-stopWrites:
						recordsAdded.Add(int32(added))
						return
					default:
					}

					batch := slicey.Random(batches)
					topicName := slicey.Random(topicNames)

					// Act
					offsets, err := s.AddRecords(topicName, batch)
					require.NoError(t, err)

					// Assert
					require.Equal(t, batch.Len(), len(offsets))

					verifications <- verification{
						topicName: topicName,
						offset:    offsets[0],
						records:   tester.BatchIndividualRecords(t, batch, 0, batch.Len()),
					}

					added += batch.Len()
				}
			}()
		}

		// concurrently add records using AddRecord()
		for range singleAdders {
			go func() {
				defer wg.Done()

				added := 0
				for {
					select {
					case <-stopWrites:
						recordsAdded.Add(int32(added))
						return
					default:
					}

					expectedRecord := tester.BatchIndividualRecords(t, slicey.Random(batches), 0, 1)[0]

					topicName := slicey.Random(topicNames)

					// Act
					offset, err := s.AddRecord(topicName, expectedRecord)
					require.NoError(t, err)

					// Assert
					verifications <- verification{
						topicName: topicName,
						offset:    offset,
						records:   [][]byte{expectedRecord},
					}

					added += len(expectedRecord)
				}
			}()
		}

		// concurrently verify the records that were written
		for range verifiers {
			go func() {
				for verification := range verifications {
					// Act
					gotRecords, err := s.GetRecords(ctx, verification.topicName, verification.offset, len(verification.records), 0)
					require.NoError(t, err)

					// Assert
					require.Equal(t, len(verification.records), len(gotRecords))
					for i, expected := range verification.records {
						got := gotRecords[i]
						require.Equal(t, expected, got)
					}
				}
			}()
		}

		// Run workers concurrently for a while
		time.Sleep(250 * time.Millisecond)

		// stop writes and wait for all writers to return
		close(stopWrites)
		wg.Wait()

		// stop verifiers once they've verified all writes
		close(verifications)

		// assert that some minimum amount of records were added concurrently
		added := int(recordsAdded.Load())
		require.Greater(t, added, 5_000)
	})
}
