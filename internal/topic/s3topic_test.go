package topic_test

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/micvbang/go-helpy/stringy"
	seb "github.com/micvbang/simple-event-broker"
	"github.com/micvbang/simple-event-broker/internal/infrastructure/tester"
	"github.com/micvbang/simple-event-broker/internal/topic"
	"github.com/stretchr/testify/require"
)

// TestS3WriteToS3 verifies that Writer creates an io.WriteCloser that calls
// S3's PutObject method with the given data once the io.WriteCloser is closed.
func TestS3WriteToS3(t *testing.T) {
	bucketName := "mybucket"
	recordBatchPath := "topicName/000123.record_batch"
	randomBytes := []byte(stringy.RandomN(500))

	s3Mock := &tester.S3Mock{}
	s3Mock.MockPutObject = func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		// Verify the expected parameters are passed on to S3
		require.Equal(t, *input.Bucket, bucketName)
		require.Equal(t, *input.Key, recordBatchPath)

		gotBody, err := io.ReadAll(input.Body)
		require.NoError(t, err)
		require.EqualValues(t, randomBytes, gotBody)

		return nil, nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, bucketName, "")

	// Act
	rbWriter, err := s3Storage.Writer(recordBatchPath)
	require.NoError(t, err)

	n, err := rbWriter.Write(randomBytes)
	require.NoError(t, err)
	require.Equal(t, len(randomBytes), n)

	// Assert
	// file should not be written to s3 before it's closed
	require.False(t, s3Mock.PutObjectCalled)

	// file should be written to s3 when it's closed
	err = rbWriter.Close()
	require.NoError(t, err)
	require.True(t, s3Mock.PutObjectCalled)
}

// TestS3WriteWithPrefix verifies that the given prefix is used when calling
// S3's PutObject.
func TestS3WriteWithPrefix(t *testing.T) {
	expectedBytes := tester.RandomBytes(t, 512)

	const (
		s3KeyPrefix     = "some-prefix"
		recordBatchPath = "topicName/000123.record_batch"
	)
	expectedKey := path.Join(s3KeyPrefix, recordBatchPath)

	s3Mock := &tester.S3Mock{}
	s3Mock.MockPutObject = func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		// Verify the expected parameters are passed on to S3
		require.Equal(t, expectedKey, *input.Key)

		return nil, nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "some-prefix")

	// Act
	wtr, err := s3Storage.Writer(recordBatchPath)
	require.NoError(t, err)
	tester.WriteAndClose(t, wtr, expectedBytes)
	require.True(t, s3Mock.PutObjectCalled)
}

// TestS3ReadFromS3 verifies that Reader returns an io.Reader that returns
// calls S3's GetObject method, returning the bytes that were fetched from S3.
func TestS3ReadFromS3(t *testing.T) {
	recordBatchPath := "topicName/000123.record_batch"
	expectedBytes := tester.RandomBytes(t, 512)

	s3Mock := &tester.S3Mock{}
	s3Mock.MockGetObject = func(goi *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewBuffer(expectedBytes)),
		}, nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "")

	// Act
	rdr, err := s3Storage.Reader(recordBatchPath)
	require.NoError(t, err)
	defer rdr.Close()

	// Assert
	gotBytes, err := io.ReadAll(rdr)
	require.NoError(t, err)

	require.Equal(t, expectedBytes, gotBytes)
}

// TestS3ReadWithPrefix verifies that the given prefix is used when calling S3's
// GetObject.
func TestS3ReadWithPrefix(t *testing.T) {
	expectedBytes := tester.RandomBytes(t, 512)

	const (
		s3KeyPrefix     = "some-prefix"
		recordBatchPath = "topicName/000123.record_batch"
	)
	expectedPath := path.Join(s3KeyPrefix, recordBatchPath)

	s3Mock := &tester.S3Mock{}
	s3Mock.MockGetObject = func(goi *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		// Verify the expected parameters are passed on to S3
		require.Equal(t, expectedPath, *goi.Key)
		return &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewBuffer(expectedBytes)),
		}, nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "some-prefix")

	// Act
	rdr, err := s3Storage.Reader(recordBatchPath)
	require.NoError(t, err)

	tester.ReadAndClose(t, rdr)
	require.True(t, s3Mock.GetObjectCalled)
}

// TestListFiles verifies that ListFiles returns a list of the files outputted
// by S3's ListObjectsPages's successive calls to the provided callback.
func TestListFiles(t *testing.T) {
	listObjectOutputBatches := [][]topic.File{
		{
			{Path: "dummy1/name1.ext", Size: 101},
			{Path: "dummy1/name2.ext", Size: 102},
			{Path: "dummy1/name3.ext", Size: 103},
		},
		{
			{Path: "dummy2/name1.ext", Size: 201},
			{Path: "dummy2/name2.ext", Size: 202},
			{Path: "dummy2/name3.ext", Size: 203},
		},
		{
			{Path: "dummy3/name1.ext", Size: 301},
		},
	}

	expectedFiles := []topic.File{}
	for _, batch := range listObjectOutputBatches {
		expectedFiles = append(expectedFiles, batch...)
	}

	s3Mock := &tester.S3Mock{}
	s3Mock.MockListObjectPages = func(input *s3.ListObjectsInput, f func(*s3.ListObjectsOutput, bool) bool) error {
		for i, listObjectBatch := range listObjectOutputBatches {
			listObjectsOutput := listObjectsOutputFromFiles(listObjectBatch)
			lastPage := i == len(listObjectOutputBatches)-1

			more := f(listObjectsOutput, lastPage)
			if !more {
				break
			}
		}

		return nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "")

	gotFiles, err := s3Storage.ListFiles("dummy/dir", ".ext")
	require.NoError(t, err)

	require.Equal(t, expectedFiles, gotFiles)
}

// TestListFilesOverlappingNames verifies that ListFiles formats Prefix in
// requests to S3 ListObjectPages correctly, i.e. removes any prefix "/", and
// ensures that it has "/" as a suffix.
func TestListFilesOverlappingNames(t *testing.T) {
	mockListObjectPagesCalled := 0

	s3Mock := &tester.S3Mock{}
	s3Mock.MockListObjectPages = func(input *s3.ListObjectsInput, f func(*s3.ListObjectsOutput, bool) bool) error {
		// Assert
		require.False(t, strings.HasPrefix(*input.Prefix, "/"))
		require.True(t, strings.HasSuffix(*input.Prefix, "/"))

		mockListObjectPagesCalled += 1

		return nil
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "")

	// Act
	testPrefixes := []string{
		"dummy/dir",
		"/dummy/dir",
		"dummy/dir/",
		"/dummy/dir/",
	}

	for _, prefix := range testPrefixes {
		t.Run(fmt.Sprintf("prefix '%s'", prefix), func(t *testing.T) {
			_, err := s3Storage.ListFiles(prefix, ".ext")
			require.NoError(t, err)
		})
	}

	// Assert that MockListObjectPages has been called (and its assertions have
	// been run)
	require.Equal(t, len(testPrefixes), mockListObjectPagesCalled)
}

func listObjectsOutputFromFiles(files []topic.File) *s3.ListObjectsOutput {
	s3Objects := make([]*s3.Object, len(files))

	for i := range files {
		s3Objects[i] = &s3.Object{
			Key:  &files[i].Path,
			Size: &files[i].Size,
		}
	}

	return &s3.ListObjectsOutput{
		Contents: s3Objects,
	}
}

func TestS3ReadFromS3NotFound(t *testing.T) {
	recordBatchPath := "topicName/000123.record_batch"

	s3Mock := &tester.S3Mock{}
	s3Mock.MockGetObject = func(goi *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return nil, awserr.New(s3.ErrCodeNoSuchKey, "", nil)
	}

	s3Storage := topic.NewS3Storage(log, s3Mock, "mybucket", "")

	// Act
	_, err := s3Storage.Reader(recordBatchPath)

	// Assert
	require.ErrorIs(t, err, seb.ErrNotInStorage)
}