package recordbatch

import (
	"encoding/binary"
	"fmt"
	"io"
)

var (
	FileFormatMagicBytes = [3]byte{'s', 'l', 'c'}
	byteOrder            = binary.LittleEndian
)

const (
	FileFormatVersion = 1
	headerSize        = 9
	recordIndexSize   = 4
)

type Header struct {
	MagicBytes [3]byte
	Version    int16
	NumRecords uint32
}

// Write writes a RecordBatch file to wtr, consisting of a header, a record
// index, and the given records.
func Write(wtr io.Writer, records [][]byte) error {
	header := Header{
		MagicBytes: FileFormatMagicBytes,
		Version:    FileFormatVersion,
		NumRecords: uint32(len(records)),
	}

	err := binary.Write(wtr, byteOrder, header)
	if err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	var recordIndex uint32
	for _, record := range records {
		err = binary.Write(wtr, byteOrder, recordIndex)
		if err != nil {
			return fmt.Errorf("writing record index %d: %w", recordIndex, err)
		}
		recordIndex += uint32(len(record))
	}

	for i, record := range records {
		err = binary.Write(wtr, byteOrder, record)
		if err != nil {
			return fmt.Errorf("writing record %d/%d: %w", i+1, len(records), err)
		}
	}
	return nil
}

var ErrOutOfBounds = fmt.Errorf("attempting to read out of bounds record")

type RecordBatch struct {
	header      Header
	recordIndex []uint32
	rdr         io.ReadSeeker
}

// Parse parses a RecordBatch file and returns a RecordBatch which can be used
// to read individual records.
func Parse(rdr io.ReadSeeker) (*RecordBatch, error) {
	header := Header{}
	err := binary.Read(rdr, byteOrder, &header)
	if err != nil {
		return nil, fmt.Errorf("reading header: %w", err)
	}

	recordIndices := make([]uint32, header.NumRecords)
	err = binary.Read(rdr, byteOrder, &recordIndices)
	if err != nil {
		return nil, fmt.Errorf("reading record index: %w", err)
	}

	return &RecordBatch{
		header:      header,
		recordIndex: recordIndices,
		rdr:         rdr,
	}, nil
}

func (rb *RecordBatch) Record(recordIndex uint32) ([]byte, error) {
	if recordIndex >= rb.header.NumRecords {
		return nil, fmt.Errorf("%d records available, record index %d does not exist: %w", rb.header.NumRecords, recordIndex, ErrOutOfBounds)
	}

	recordOffset := rb.recordIndex[recordIndex]

	fileOffset := headerSize + rb.header.NumRecords*recordIndexSize + recordOffset
	_, err := rb.rdr.Seek(int64(fileOffset), io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("seeking for record %d/%d: %w", recordIndex, len(rb.recordIndex), err)
	}

	// last record, read the remainder of the file
	if recordIndex == uint32(len(rb.recordIndex)-1) {
		return io.ReadAll(rb.rdr)
	}

	// read record bytes
	size := rb.recordIndex[recordIndex+1] - recordOffset
	buf := make([]byte, size)
	_, err = io.ReadFull(rb.rdr, buf)
	if err != nil {
		return nil, fmt.Errorf("reading record: %w", err)
	}

	return buf, nil
}
