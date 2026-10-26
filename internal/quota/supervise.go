package quota

import (
	"context"
	"time"

	"github.com/ph03iA/CarryOn/internal/winjob"
)

// supervise terminates a probe when its timeout expires or the context ends.
// Reads block on stdout, so waiting for the process is not enough.
func supervise(ctx context.Context, process *winjob.Process, timeout time.Duration) func() {
	timer := time.AfterFunc(timeout, process.Terminate)
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			process.Terminate()
		case <-done:
		}
	}()
	return func() {
		timer.Stop()
		close(done)
	}
}
