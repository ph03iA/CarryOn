package logx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// Follow writes existing complete lines from path to out, then streams new
// lines until ctx is cancelled. A missing file is waited for.
func Follow(ctx context.Context, path string, out io.Writer, poll time.Duration) error {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	var offset int64
	var pending []byte
	for {
		if err := readNew(path, out, &offset, &pending); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// readNew copies bytes appended since offset and emits whole lines only.
func readNew(path string, out io.Writer, offset *int64, pending *[]byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < *offset {
		*offset = 0
		*pending = nil
	}
	if info.Size() == *offset {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(*offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, info.Size()-*offset))
	if err != nil {
		return err
	}
	*offset += int64(len(data))
	*pending = append(*pending, data...)

	if index := bytes.LastIndexByte(*pending, '\n'); index >= 0 {
		if _, err := out.Write((*pending)[:index+1]); err != nil {
			return err
		}
		*pending = append([]byte{}, (*pending)[index+1:]...)
	}
	return nil
}
