package session

import (
	"bytes"
	"io"
	"os"
)

// line is one JSONL record with its absolute byte offset.
type line struct {
	Offset int64
	End    int64
	Data   []byte
}

// readTail reads at most maxBytes from the end of path, dropping a partial
// first line so every returned record parses.
func readTail(path string, maxBytes int64) ([]line, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	start := int64(0)
	if maxBytes > 0 && size > maxBytes {
		start = size - maxBytes
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, size-start))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			start += int64(index + 1)
			data = data[index+1:]
		}
	}
	return splitLines(data, start), nil
}

// splitLines records each non-empty line with its offset.
func splitLines(data []byte, base int64) []line {
	lines := make([]line, 0, 64)
	offset := base
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		var chunk []byte
		if index < 0 {
			chunk, data = data, nil
		} else {
			chunk, data = data[:index], data[index+1:]
		}
		end := offset + int64(len(chunk))
		trimmed := bytes.TrimSpace(chunk)
		if len(trimmed) > 0 {
			lines = append(lines, line{Offset: offset, End: end, Data: trimmed})
		}
		offset = end + 1
	}
	return lines
}

// readFrom returns the records that begin at or after offset.
func readFrom(path string, offset int64) ([]line, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return splitLines(data, offset), nil
}

// readHead reads the first maxBytes of a log, keeping full lines.
func readHead(path string, maxBytes int64) ([]line, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return nil, err
	}
	return splitLines(data, 0), nil
}
