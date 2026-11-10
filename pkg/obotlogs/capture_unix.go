//go:build unix

package obotlogs

import (
	"bufio"
	"errors"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type captured struct {
	fd       int
	original *os.File // a duplicate of the real stream
	pipe     *os.File // the write end now installed as fd
	done     chan struct{}
}

var (
	captureMu sync.Mutex
	active    []*captured
)

// Capture tees everything the process writes to stdout and stderr, at the file descriptor
// level, into the default buffer, while still writing it to the real streams. Taking over the
// descriptors catches every writer: slog, the standard logger, dependencies that captured
// os.Stderr at init, and child processes that inherit the descriptors.
//
// Crash output goes to the real stderr directly as well, so a fatal panic is never lost in
// the pipe. Call Stop before exiting on purpose so the last lines are flushed.
func Capture() error {
	captureMu.Lock()
	defer captureMu.Unlock()
	if len(active) > 0 {
		return nil
	}
	var started []*captured
	for _, s := range []struct {
		fd   int
		name string
	}{{fd: 1, name: "stdout"}, {fd: 2, name: "stderr"}} {
		c, err := tee(s.fd, s.name, std)
		if err != nil {
			for _, c := range started {
				c.stop()
			}
			return err
		}
		started = append(started, c)
		if s.fd == 2 {
			_ = debug.SetCrashOutput(c.original, debug.CrashOptions{})
		}
	}
	active = started
	return nil
}

// Stop restores the real streams and waits for buffered output to be written to them.
func Stop() {
	captureMu.Lock()
	defer captureMu.Unlock()
	for _, c := range active {
		c.stop()
	}
	active = nil
}

func tee(fd int, name string, buf *Buffer) (*captured, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	original := os.NewFile(uintptr(dup), name+"-original")
	r, w, err := os.Pipe()
	if err != nil {
		_ = original.Close()
		return nil, err
	}
	if err := unix.Dup2(int(w.Fd()), fd); err != nil {
		_ = original.Close()
		_ = r.Close()
		_ = w.Close()
		return nil, err
	}
	c := &captured{fd: fd, original: original, pipe: w, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer r.Close()
		copyLines(r, original, name, buf)
	}()
	return c, nil
}

// copyLines forwards everything from r to out as it arrives and records complete lines.
func copyLines(r io.Reader, out io.Writer, name string, buf *Buffer) {
	reader := bufio.NewReaderSize(r, 64*1024)
	var partial []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(chunk) > 0 {
			_, _ = out.Write(chunk)
			if chunk[len(chunk)-1] == '\n' {
				if len(partial) > 0 {
					buf.Add(name, string(append(partial, chunk...)), time.Now())
					partial = partial[:0]
				} else {
					buf.Add(name, string(chunk), time.Now())
				}
			} else if len(partial) < maxLineBytes {
				partial = append(partial, chunk...)
			}
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			if len(partial) > 0 {
				buf.Add(name, string(partial), time.Now())
			}
			return
		}
	}
}

func (c *captured) stop() {
	// Point the descriptor back at the real stream, then close our write end: the reader sees
	// EOF once everything already written has drained.
	_ = unix.Dup2(int(c.original.Fd()), c.fd)
	_ = c.pipe.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}
