package crossmem

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// exportConversations writes one qa.jsonl of full Q&A across sessions. Workers
// read transcripts in parallel and a single writer flushes to a temp file that
// is renamed into place.
func (c *Client) exportConversations(opts ConvExportOptions) (ConvExportResult, error) {
	if opts.Provider == "" {
		opts.Provider = "all"
	}
	out := opts.Out
	if out == "" && opts.CWD != "" {
		if abs, err := filepath.Abs(opts.CWD); err == nil {
			out = filepath.Join(abs, ".crossmem")
		}
	}
	if out == "" {
		out = c.defaultDumpDir()
	}
	out = expandPath(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return ConvExportResult{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = allSessionsLimit
	}
	sessions, err := c.listSessions(ListOptions{
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
			for _, pair := range c.sessionQA(session) {
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

// ConvImportOptions configures ImportConversations.
type ConvImportOptions struct {
	In     string
	Out    string
	CWD    string
	DryRun bool
	Merge  bool
}

// ConvImportResult is what conversation import wrote or would write.
type ConvImportResult struct {
	Source  string `json:"source"`
	QAFile  string `json:"qaFile"`
	QAPairs int    `json:"qaPairs"`
	Added   int    `json:"added,omitempty"`
	DryRun  bool   `json:"dryRun,omitempty"`
	Merged  bool   `json:"merged,omitempty"`
}

// importConversations copies a qa.jsonl into the destination directory.
// --in may be the file itself or a directory that contains qa.jsonl.
func (c *Client) importConversations(opts ConvImportOptions) (ConvImportResult, error) {
	src, err := c.resolveQAFile(opts.In)
	if err != nil {
		return ConvImportResult{}, err
	}
	pairs, err := inspectQAFile(src)
	if err != nil {
		return ConvImportResult{}, err
	}

	out := opts.Out
	if out == "" && opts.CWD != "" {
		if abs, err := filepath.Abs(opts.CWD); err == nil {
			out = filepath.Join(abs, ".crossmem")
		}
	}
	if out == "" {
		out = c.defaultDumpDir()
	}
	out = expandPath(out)
	dest := filepath.Join(out, qaJSONLName)
	result := ConvImportResult{Source: src, QAFile: dest, QAPairs: pairs, DryRun: opts.DryRun, Merged: opts.Merge}
	if samePath(src, dest) {
		if !opts.Merge {
			result.Added = pairs
		}
		return result, nil
	}

	if opts.DryRun {
		if opts.Merge {
			added, err := countNewQALines(src, dest)
			if err != nil {
				return ConvImportResult{}, err
			}
			result.Added = added
		} else {
			result.Added = pairs
		}
		return result, nil
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return ConvImportResult{}, err
	}
	if opts.Merge {
		added, err := mergeQAFile(src, dest)
		if err != nil {
			return ConvImportResult{}, err
		}
		result.Added = added
		return result, nil
	}
	if err := atomicCopy(src, dest); err != nil {
		return ConvImportResult{}, err
	}
	result.Added = pairs
	return result, nil
}

func samePath(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(ai, bi)
}

func (c *Client) resolveQAFile(in string) (string, error) {
	if in == "" {
		in = c.defaultDumpDir()
	}
	in = expandPath(in)
	info, err := os.Stat(in)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		in = filepath.Join(in, qaJSONLName)
	}
	if _, err := os.Stat(in); err != nil {
		return "", err
	}
	return in, nil
}

func inspectQAFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	pairs := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec QARecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return 0, fmt.Errorf("%s is not a qa.jsonl: %w", path, err)
		}
		if rec.SessionID == "" && rec.Q == "" {
			return 0, fmt.Errorf("%s is not a qa.jsonl: missing sessionId and q", path)
		}
		pairs++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return pairs, nil
}

func qaKey(rec QARecord) string {
	return rec.SessionID + "\x00" + rec.Time + "\x00" + rec.Q
}

func loadQAKeys(path string) (map[string]struct{}, error) {
	keys := map[string]struct{}{}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return keys, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec QARecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		keys[qaKey(rec)] = struct{}{}
	}
	return keys, scanner.Err()
}

func countNewQALines(src, dest string) (int, error) {
	existing, err := loadQAKeys(dest)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	added := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec QARecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if _, ok := existing[qaKey(rec)]; !ok {
			added++
		}
	}
	return added, scanner.Err()
}

func mergeQAFile(src, dest string) (int, error) {
	existing, err := loadQAKeys(dest)
	if err != nil {
		return 0, err
	}
	srcFile, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer srcFile.Close()

	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	if destFile, err := os.Open(dest); err == nil {
		_, copyErr := io.Copy(out, destFile)
		destFile.Close()
		if copyErr != nil {
			out.Close()
			_ = os.Remove(tmp)
			return 0, copyErr
		}
	} else if !os.IsNotExist(err) {
		out.Close()
		return 0, err
	}

	writer := bufio.NewWriterSize(out, writeBufSize)
	scanner := bufio.NewScanner(srcFile)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	added := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec QARecord
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		key := qaKey(rec)
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		if _, err := writer.Write(append(append([]byte{}, line...), '\n')); err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return 0, err
		}
		added++
	}
	if err := scanner.Err(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := writer.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return added, nil
}

func atomicCopy(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
