package raft

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)


// Distinguishes the two kinds of records we write
type walRecordType uint8

const (
	walRecordState walRecordType = iota + 1 // currentTerm + votedFor
	walRecordEntry // single LogEntry
)


// walState is what we persist for the node's durable state.
// Raft requires these two fields to survive crashes.
type walState struct {
	Term uint64
	votedFor string
}


// WAL is a write-ahead log backed by a single append-only file.
// Every durable option appends a record to this file.
type WAL struct {
	file *os.File
	writer *bufio.Writer
	path string
}

// Opens an existing WAL file or creates a new one.
// dir is the DataDir from Config (e.g., "data/node1").
func Open(dir string) (*WAL, error) {
	// Create a directory and all parents. No error if already exists.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "raft.wal")


	// os.O_CREATE | os.O_RDWR | os.O_APPEND:
    // Create if not exists, open for read+write, all writes go to end of file.
    // 0644 = owner can read/write, group and others can read.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &WAL{
		file: file,
		writer: bufio.NewWriter(file),
		path: path,
	}, nil
}



// Persists currentTerm and votedFor to the WAL.
// MUST be called (and Sync called after) before casting any vote.
// This is the critical path: if the node crashes but before Sync,
// the vote is lost - which is safe (it can vote again).
// If it crashes after Sync, the vote is durable - which prevents
// double voting on restart
func (w *WAL) SaveState(term uint64, votedFor string) error {
	// Encode: 1 byte type + 8 bytes term + 4 bytes string length + N bytes string
	votedForBytes := []byte(votedFor)
	record := make([]byte, 1+8+4+len(votedForBytes))

	record[0] = byte(walRecordState)
	binary.BigEndian.PutUint64(record[1:9], term)
	binary.BigEndian.PutUint32(record[9:13], uint32(len(votedForBytes)))
	copy(record[13:], votedForBytes)

	return w.writeRecord(record)
}


// Persists a single LogEntry to the WAL.
// Must be called before sending and AppendEntries ack.
func (w *WAL) SaveEntry(entry LogEntry) error {
	return w.SaveEntries([]LogEntry{entry})
}


// Persists multiple LogEntries in one pass
func (w *WAL) SaveEntries(entries []LogEntry) error {
	for _, entry := range entries {
		// Encode: 1 byte type + 8 bytes index + 8 bytes term + 1 byte entryType + 4 bytes data length + N bytes data
		record := make([]byte, 1+8+8+1+4+len(entry.Data))
		record[0] = byte(walRecordEntry)
		binary.BigEndian.PutUint64(record[1:9], entry.Index)
		binary.BigEndian.PutUint64(record[9:17], entry.Term)
		record[17] = byte(entry.Type)
		binary.BigEndian.PutUint32(record[18:22], uint32(len(entry.Data)))
		copy(record[22:], entry.Data)

		if err := w.writeRecord(record); err != nil {
			return err
		}
	}

	return nil
}


// Appends one record to the WAL with length prefix and CRC32 checksum.
// Format: [ 4 bytes length ][ N bytes data ][ 4 bytes CRC32 ]
func (w *WAL) writeRecord(data []byte) error {
	// Write length prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := w.writer.Write(lenBuf); err != nil {
		return err
	}

	// Write data
	if _, err := w.writer.Write(data); err != nil {
		return err
	}

	// Write CRC32 checksum - computed over the data bytes only
	checksum := crc32.ChecksumIEEE(data)
	crcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBuf, checksum)
	if _, err := w.writer.Write(crcBuf); err != nil {
		return err
	}

	return nil
}


// Flushes the OS buffer to disk (fsync).
// This is the line between "probably written" and "durably written".
// Expensive - takes ~1ms on an SSD. This is why WAL fsync latency is one of our 
// Prometheus metrics.
func (w *WAL) Sync() error {
	// Flush bufio.Writer to the underlying file first
	if err := w.writer.Flush(); err != nil {
		return err
	}

	// Then fsync - forces OS to flush its page cache to physical disk
	return w.file.Sync()
}


// Reads the entire WAL and reconstructs the last known state plus all log entries.
// Called once on startup/restart.
// Records with invalid checksum mark the recovery boundary - everything from that
// point on is discarded (torn write defense).
func (w * WAL) ReadAll() (term uint64, votedFor string, entries []LogEntry, err error) {
	// Seek to beginning of file for reading
	if _, err = w.file.Seek(0, io.SeekStart); err != nil {
		return
	}

	reader := bufio.NewReader(w.file)

	for {
		// Read 4-byte length prefix
		lenBuf := make([]byte, 4)
		if _, readErr := io.ReadFull(reader, lenBuf); readErr != nil {
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				break // clean end of file or partial record - stop here
			}
			err = readErr
			return
		}

		dataLen := binary.BigEndian.Uint32(lenBuf)

		// Read data bytes
		data := make([]byte, dataLen)
		if _, readErr := io.ReadFull(reader, data); readErr != nil {
			break // partial record - treat as recovery boundary
		}

		// Read 4-byte CRC32
		crcBuf := make([]byte, 4)
		if _, readErr := io.ReadFull(reader, crcBuf); readErr != nil {
			break // partial checksum - treat as recovery boundary
		}

		// Verify checksum - if it doesn't match, this record is corrupt
		expected := binary.BigEndian.Uint32(crcBuf)
		actual := crc32.ChecksumIEEE(data)
		if expected != actual {
			break // torn write detected - discard this and everything after
		}

		// Decode the record based on its type byte
		switch walRecordType(data[0]) {
		case walRecordState:
			term = binary.BigEndian.Uint64(data[1:9])
			strLen := binary.BigEndian.Uint32(data[9:13])
			votedFor = string(data[13 : 13+strLen])
		
		case walRecordEntry:
			entry := LogEntry{}
			entry.Index = binary.BigEndian.Uint64(data[1:9])
			entry.Term = binary.BigEndian.Uint64(data[9:17])
			entry.Type = EntryType(data[17])
			dataLen := binary.BigEndian.Uint32(data[18:22])
			entry.Data = make([]byte, dataLen)
			copy(entry.Data, data[22:22+dataLen])
			entries = append(entries, entry)
		}
	}

	return
}


// Removes all WAL entries from the given log index onward.
// Called when the leader tells a follower its log has a conflict.
// Strategy: read all valid records, rewrite the file keeping only
// records with index < the truncation point.
func (w *WAL) TruncateFrom(index uint64) error {
	term, votedFor, entries, err := w.ReadAll()

	if err != nil {
		return err
	}

	// Close and recreate the file (truncate to zero, then rewrite)
	w.file.Close()
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	w.file = file
	w.writer = bufio.NewWriter(file)

	// Rewrite state record
	if err := w.SaveState(term, votedFor); err != nil {
		return err
	}

	// Rewrite only entries before the truncation index
	for _, entry := range entries {
		if entry.Index < index {
			if err := w.SaveEntry(entry); err != nil {
				return err
			}
		}
	}

	return w.Sync()
}


// Closes the underlying file
func (w *WAL) Close() error {
	if err := w.writer.Flush(); err != nil {
		return err
	}

	return w.file.Close()
}