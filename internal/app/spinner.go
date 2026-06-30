package app

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mattn/go-isatty"
)

// spinner is a tiny loading indicator drawn on stderr while crossmem reads the
// stores in parallel. It only animates when stderr is an interactive terminal,
// so piped/agent invocations stay clean (the bundle on stdout is untouched).
type spinner struct {
	stop chan struct{}
	done chan struct{}
}

func startSpinner(w io.Writer, label string) *spinner {
	f, ok := w.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return nil
	}
	s := &spinner{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
		ticker := time.NewTicker(90 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Fprint(f, "\r\033[K")
				close(s.done)
				return
			case <-ticker.C:
				fmt.Fprintf(f, "\r%c %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return s
}

func (s *spinner) Stop() {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
}
