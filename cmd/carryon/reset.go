package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ph03iA/CarryOn/internal/config"
	"github.com/ph03iA/CarryOn/internal/single"
)

const resetPrompt = "Delete CarryOn's state? Config and keys are kept. [y/N] "

func runReset(_ context.Context, paths config.Paths, args []string) error {
	lock, err := single.Acquire(single.Name)
	if err != nil {
		return fmt.Errorf("stop CarryOn before resetting its state: %w", err)
	}
	defer lock.Release()

	if !hasFlag(args, "--yes") {
		confirmed, err := confirmReset(os.Stdin, os.Stdout)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	if err := resetState(paths); err != nil {
		return err
	}
	fmt.Println("State deleted. Config and keys were kept.")
	return nil
}

// confirmReset asks once and accepts y or yes, in any case.
func confirmReset(in io.Reader, out io.Writer) (bool, error) {
	return confirmPrompt(in, out, resetPrompt)
}

// confirmPrompt prints prompt and accepts y or yes, in any case.
func confirmPrompt(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// resetState deletes the SQLite database and its side files. Config, keys,
// and the log stay.
func resetState(paths config.Paths) error {
	for _, path := range []string{paths.Database, paths.Database + "-wal", paths.Database + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete %s: %w", path, err)
		}
	}
	return nil
}
