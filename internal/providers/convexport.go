package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	qaJSONLName      = "qa.jsonl"
	allSessionsLimit = 100000
	writeBufSize     = 16 << 20
)

// Message is one turn inside an exchange. role is user, assistant, tool, or thinking.
type Message struct {
	Role    string `json:"role"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
}

// QARecord is one question plus everything that followed until the next question.
// No agent, model, provider, or token fields. messages keeps tools, results, and thinking.
type QARecord struct {
	SessionID string    `json:"sessionId"`
	Folder    string    `json:"folder,omitempty"`
	Q         string    `json:"q"`
	A         string    `json:"a"`
	Time      string    `json:"time"`
	Messages  []Message `json:"messages,omitempty"`
}

// ConvExportOptions configures ExportConversations.
type ConvExportOptions struct {
	Out      string
	Provider string
	CWD      string
	Limit    int
}

// ConvExportResult is what conversation export wrote.
type ConvExportResult struct {
	Out      string `json:"out"`
	Sessions int    `json:"sessions"`
	QAPairs  int    `json:"qaPairs"`
	QAFile   string `json:"qaFile"`
}

// ExportConversations writes one qa.jsonl of full Q&A across sessions. Workers
// read transcripts in parallel and a single writer flushes to a temp file that
// is renamed into place.
func ExportConversations(opts ConvExportOptions) (ConvExportResult, error) {
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	out := opts.Out
	if out == "" {
		out = DefaultDumpDir()
	}
	out = expandPath(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return ConvExportResult{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = allSessionsLimit
	}
	sessions, err := ListSessions(ListOptions{
		Provider:       opts.Provider,
		CWD:            opts.CWD,
		Limit:          limit,
		IncludeCurrent: true,
	})
	if err != nil {
		return ConvExportResult{}, err
	}

	qaPath := filepath.Join(out, qaJSONLName)
	tmpPath := qaPath + ".tmp"
	_ = os.Remove(tmpPath)
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return ConvExportResult{}, err
	}

	writer := bufio.NewWriterSize(file, writeBufSize)
	lines := make(chan []byte, 64)
	var writeErr error
	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		for line := range lines {
			if writeErr != nil {
				continue
			}
			if _, err := writer.Write(line); err != nil {
				writeErr = err
			}
		}
	}()

	workers := runtime.GOMAXPROCS(0) * 8
	if workers < 16 {
		workers = 16
	}
	if workers > 64 {
		workers = 64
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var pairs atomic.Int64

	for i := range sessions {
		session := sessions[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, pair := range sessionQA(session) {
				line, err := marshalQA(pair)
				if err != nil {
					continue
				}
				pairs.Add(1)
				lines <- line
			}
		}()
	}
	wg.Wait()
	close(lines)
	writerDone.Wait()

	if writeErr != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return ConvExportResult{}, writeErr
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return ConvExportResult{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return ConvExportResult{}, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return ConvExportResult{}, err
	}
	if err := os.Rename(tmpPath, qaPath); err != nil {
		_ = os.Remove(tmpPath)
		return ConvExportResult{}, err
	}

	return ConvExportResult{
		Out:      out,
		Sessions: len(sessions),
		QAPairs:  int(pairs.Load()),
		QAFile:   qaPath,
	}, nil
}

var qaBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func marshalQA(pair QARecord) ([]byte, error) {
	buf := qaBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(pair)
	if err != nil {
		qaBufPool.Put(buf)
		return nil, err
	}
	line := make([]byte, buf.Len())
	copy(line, buf.Bytes())
	qaBufPool.Put(buf)
	return line, nil
}
