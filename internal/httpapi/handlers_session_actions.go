package httpapi

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type rawSessionLine struct {
	SessionID string `json:"sessionId"`
}

type sessionDeletePreviewFile struct {
	SourceFile   string `json:"source_file"`
	Kind         string `json:"kind,omitempty"`
	Change       string `json:"change,omitempty"`
	RemovedLines int    `json:"removed_lines"`
}

type sessionDeletePreview struct {
	SessionID    string                     `json:"session_id"`
	FileCount    int                        `json:"file_count"`
	RemovedLines int                        `json:"removed_lines"`
	Files        []sessionDeletePreviewFile `json:"files"`
}

type sessionBundleManifest struct {
	Kind                string    `json:"kind,omitempty"`
	Version             int       `json:"version"`
	SessionID           string    `json:"session_id,omitempty"`
	ProjectSlug         string    `json:"project_slug,omitempty"`
	TranscriptRelPath   string    `json:"transcript_rel_path,omitempty"`
	ExportedAt          time.Time `json:"exported_at"`
	TranscriptLines     int       `json:"transcript_lines,omitempty"`
	HistoryLines        int       `json:"history_lines,omitempty"`
	IncludesSessionEnv  bool      `json:"includes_session_env,omitempty"`
	IncludesFileHistory bool      `json:"includes_file_history,omitempty"`
	ProjectFiles        int       `json:"project_files,omitempty"`
}

type bundleFileEntry struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

type importedSessionBundle struct {
	Manifest    sessionBundleManifest
	Transcript  []byte
	History     []byte
	SessionEnv  []bundleFileEntry
	FileHistory []bundleFileEntry
}

type importedAllSessionsBundle struct {
	Manifest    sessionBundleManifest
	Projects    []bundleFileEntry
	History     []byte
	SessionEnv  []bundleFileEntry
	FileHistory []bundleFileEntry
}

func sessionIDFromLine(line []byte) string {
	var raw rawSessionLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return ""
	}
	return raw.SessionID
}

func partitionSessionFile(path, sessionID string) (kept [][]byte, matched [][]byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := line
			if trimmed[len(trimmed)-1] == '\n' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			out := append([]byte(nil), line...)
			if len(out) == 0 || out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
			if sessionIDFromLine(trimmed) == sessionID {
				matched = append(matched, out)
			} else {
				kept = append(kept, out)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, nil, readErr
		}
	}
	return kept, matched, nil
}

func currentStamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func backupAndRewriteSessionFile(path string, kept [][]byte, stamp string, createBackup bool) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if createBackup {
		orig, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		backupPath := fmt.Sprintf("%s.amatoken.bak.%s", path, stamp)
		if err := os.WriteFile(backupPath, orig, info.Mode().Perm()); err != nil {
			return "", err
		}
		if len(kept) == 0 {
			if err := os.Remove(path); err != nil {
				return "", err
			}
			return backupPath, nil
		}
		tmpPath := path + ".amatoken.tmp"
		if err := os.WriteFile(tmpPath, bytes.Join(kept, nil), info.Mode().Perm()); err != nil {
			return "", err
		}
		if err := os.Rename(tmpPath, path); err != nil {
			_ = os.Remove(tmpPath)
			return "", err
		}
		return backupPath, nil
	}
	if len(kept) == 0 {
		return "", os.Remove(path)
	}
	tmpPath := path + ".amatoken.tmp"
	if err := os.WriteFile(tmpPath, bytes.Join(kept, nil), info.Mode().Perm()); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return "", nil
}

func backupExistingPath(path, stamp string, createBackup bool) (string, bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if !createBackup {
		if err := os.RemoveAll(path); err != nil {
			return "", false, err
		}
		return "", true, nil
	}
	backupPath := fmt.Sprintf("%s.amatoken.bak.%s", path, stamp)
	if err := os.Rename(path, backupPath); err != nil {
		return "", false, err
	}
	return backupPath, true, nil
}

func sanitizeSessionFilename(sessionID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(sessionID)
}

func countLinesAndSessions(data []byte) (int, []string) {
	seen := map[string]struct{}{}
	var ids []string
	lines := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lines++
		id := sessionIDFromLine(line)
		if id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return lines, ids
}

func normalizeJSONLLines(data []byte) [][]byte {
	var out [][]byte
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		cp := append([]byte(nil), line...)
		cp = append(cp, '\n')
		out = append(out, cp)
	}
	return out
}

func (s *Server) claudeDataRoot() string {
	root := filepath.Clean(s.Scanner.Root)
	return filepath.Dir(root)
}

func (s *Server) historyPath() string {
	return filepath.Join(s.claudeDataRoot(), "history.jsonl")
}

func (s *Server) sessionEnvPath(sessionID string) string {
	return filepath.Join(s.claudeDataRoot(), "session-env", sessionID)
}

func (s *Server) fileHistoryPath(sessionID string) string {
	return filepath.Join(s.claudeDataRoot(), "file-history", sessionID)
}

func (s *Server) collectSessionArtifacts(ctx context.Context, sessionID string) (*sessionDeletePreview, []string, string, [][]byte, error) {
	files, err := s.Repo.SessionSourceFiles(ctx, sessionID)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if len(files) == 0 {
		return nil, nil, "", nil, os.ErrNotExist
	}
	sort.Strings(files)

	preview := &sessionDeletePreview{SessionID: sessionID}
	var transcriptRel string
	rewritableFiles := make([]string, 0, len(files))
	for _, filePath := range files {
		_, matched, err := partitionSessionFile(filePath, sessionID)
		if err != nil {
			return nil, nil, "", nil, err
		}
		if len(matched) == 0 {
			continue
		}
		if transcriptRel == "" {
			if rel, relErr := filepath.Rel(s.Scanner.Root, filePath); relErr == nil {
				transcriptRel = filepath.ToSlash(rel)
			}
		}
		preview.Files = append(preview.Files, sessionDeletePreviewFile{
			SourceFile:   filePath,
			Kind:         "transcript",
			Change:       fmt.Sprintf("remove %d line(s)", len(matched)),
			RemovedLines: len(matched),
		})
		preview.RemovedLines += len(matched)
		rewritableFiles = append(rewritableFiles, filePath)
	}
	if len(rewritableFiles) == 0 {
		return nil, nil, "", nil, os.ErrNotExist
	}

	historyPath := s.historyPath()
	var historyLines [][]byte
	if _, err := os.Stat(historyPath); err == nil {
		_, matched, err := partitionSessionFile(historyPath, sessionID)
		if err != nil {
			return nil, nil, "", nil, err
		}
		if len(matched) > 0 {
			historyLines = matched
			preview.Files = append(preview.Files, sessionDeletePreviewFile{
				SourceFile:   historyPath,
				Kind:         "history",
				Change:       fmt.Sprintf("remove %d line(s)", len(matched)),
				RemovedLines: len(matched),
			})
			preview.RemovedLines += len(matched)
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, "", nil, err
	}

	if p := s.sessionEnvPath(sessionID); pathExists(p) {
		preview.Files = append(preview.Files, sessionDeletePreviewFile{
			SourceFile: p,
			Kind:       "session-env",
			Change:     "move directory to backup",
		})
	}
	if p := s.fileHistoryPath(sessionID); pathExists(p) {
		preview.Files = append(preview.Files, sessionDeletePreviewFile{
			SourceFile: p,
			Kind:       "file-history",
			Change:     "move directory to backup",
		})
	}
	preview.FileCount = len(preview.Files)
	return preview, rewritableFiles, transcriptRel, historyLines, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func splitProjectSlug(rel string) string {
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func tarAddBytes(tw *tar.Writer, name string, mode fs.FileMode, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     int64(mode.Perm()),
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func tarAddTreeFiltered(tw *tar.Writer, root, prefix string, keep func(string, os.FileInfo) bool) error {
	return filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if keep != nil && !keep(filePath, info) {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		return tarAddBytes(tw, name, info.Mode().Perm(), data)
	})
}

func shouldExportProjectFile(filePath string, info os.FileInfo) bool {
	name := info.Name()
	if strings.Contains(name, ".amatoken.bak.") || strings.HasSuffix(name, ".amatoken.tmp") {
		return false
	}
	return true
}

func countProjectFiles(root string) (int, error) {
	count := 0
	err := filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		if shouldExportProjectFile(filePath, info) {
			count++
		}
		return nil
	})
	return count, err
}

func tarAddTree(tw *tar.Writer, root, prefix string) error {
	return filepath.Walk(root, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		return tarAddBytes(tw, name, info.Mode().Perm(), data)
	})
}

func parseSessionBundle(data []byte) (*importedSessionBundle, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	bundle := &importedSessionBundle{}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return nil, fmt.Errorf("invalid bundle path %q", hdr.Name)
		}
		payload, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		switch {
		case name == "manifest.json":
			if err := json.Unmarshal(payload, &bundle.Manifest); err != nil {
				return nil, err
			}
		case name == "transcript/session.jsonl":
			bundle.Transcript = payload
		case name == "history/history.jsonl":
			bundle.History = payload
		case strings.HasPrefix(name, "session-env/"):
			rel := strings.TrimPrefix(name, "session-env/")
			bundle.SessionEnv = append(bundle.SessionEnv, bundleFileEntry{Path: rel, Mode: fs.FileMode(hdr.Mode), Data: payload})
		case strings.HasPrefix(name, "file-history/"):
			rel := strings.TrimPrefix(name, "file-history/")
			bundle.FileHistory = append(bundle.FileHistory, bundleFileEntry{Path: rel, Mode: fs.FileMode(hdr.Mode), Data: payload})
		}
	}
	if bundle.Manifest.Kind != "" && bundle.Manifest.Kind != "session" {
		return nil, errors.New("not a session bundle")
	}
	if bundle.Manifest.SessionID == "" {
		return nil, errors.New("bundle manifest missing session_id")
	}
	if bundle.Manifest.TranscriptRelPath == "" {
		return nil, errors.New("bundle manifest missing transcript_rel_path")
	}
	if len(bytes.TrimSpace(bundle.Transcript)) == 0 {
		return nil, errors.New("bundle transcript is empty")
	}
	return bundle, nil
}

func parseAllSessionsBundle(data []byte) (*importedAllSessionsBundle, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	bundle := &importedAllSessionsBundle{}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return nil, fmt.Errorf("invalid bundle path %q", hdr.Name)
		}
		payload, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		switch {
		case name == "manifest.json":
			if err := json.Unmarshal(payload, &bundle.Manifest); err != nil {
				return nil, err
			}
		case name == "history/history.jsonl":
			bundle.History = payload
		case strings.HasPrefix(name, "projects/"):
			rel := strings.TrimPrefix(name, "projects/")
			bundle.Projects = append(bundle.Projects, bundleFileEntry{Path: rel, Mode: fs.FileMode(hdr.Mode), Data: payload})
		case strings.HasPrefix(name, "session-env/"):
			rel := strings.TrimPrefix(name, "session-env/")
			bundle.SessionEnv = append(bundle.SessionEnv, bundleFileEntry{Path: rel, Mode: fs.FileMode(hdr.Mode), Data: payload})
		case strings.HasPrefix(name, "file-history/"):
			rel := strings.TrimPrefix(name, "file-history/")
			bundle.FileHistory = append(bundle.FileHistory, bundleFileEntry{Path: rel, Mode: fs.FileMode(hdr.Mode), Data: payload})
		}
	}
	if bundle.Manifest.Kind != "all-sessions" {
		return nil, errors.New("not an all-sessions bundle")
	}
	if len(bundle.Projects) == 0 {
		return nil, errors.New("bundle has no project files")
	}
	return bundle, nil
}

func collectImportedSessionIDs(entries []bundleFileEntry) []string {
	seen := map[string]struct{}{}
	var ids []string
	for _, entry := range entries {
		if !strings.HasSuffix(strings.ToLower(entry.Path), ".jsonl") {
			continue
		}
		_, sessionIDs := countLinesAndSessions(entry.Data)
		for _, id := range sessionIDs {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func countImportedLines(entries []bundleFileEntry) int {
	total := 0
	for _, entry := range entries {
		if !strings.HasSuffix(strings.ToLower(entry.Path), ".jsonl") {
			continue
		}
		lines, _ := countLinesAndSessions(entry.Data)
		total += lines
	}
	return total
}

func safeJoin(base, rel string) (string, error) {
	target := filepath.Join(base, filepath.FromSlash(rel))
	cleanBase := filepath.Clean(base)
	cleanTarget := filepath.Clean(target)
	if cleanTarget != cleanBase && !strings.HasPrefix(cleanTarget, cleanBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes base: %s", rel)
	}
	return cleanTarget, nil
}

func writeFileReplacing(path string, data []byte, mode fs.FileMode, stamp string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	backupPath, _, err := backupExistingPath(path, stamp, true)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return "", err
	}
	return backupPath, nil
}

func mergeSessionLinesFile(path, sessionID string, incoming []byte, mode fs.FileMode) (int, error) {
	lines := normalizeJSONLLines(incoming)
	if len(lines) == 0 {
		return 0, nil
	}
	existing := map[string]struct{}{}
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 8<<20)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 || sessionIDFromLine(line) != sessionID {
				continue
			}
			existing[string(append(append([]byte(nil), line...), '\n'))] = struct{}{}
		}
		if err := scanner.Err(); err != nil {
			return 0, err
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	toAppend := make([][]byte, 0, len(lines))
	for _, line := range lines {
		key := string(line)
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		toAppend = append(toAppend, line)
	}
	if len(toAppend) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Write(bytes.Join(toAppend, nil)); err != nil {
		return 0, err
	}
	return len(toAppend), nil
}

func writeBundleTree(base string, entries []bundleFileEntry, stamp string) (string, int, error) {
	if len(entries) == 0 {
		return "", 0, nil
	}
	backupPath, _, err := backupExistingPath(base, stamp, true)
	if err != nil {
		return "", 0, err
	}
	for _, entry := range entries {
		target, err := safeJoin(base, entry.Path)
		if err != nil {
			return "", 0, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", 0, err
		}
		mode := entry.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, entry.Data, mode); err != nil {
			return "", 0, err
		}
	}
	return backupPath, len(entries), nil
}

func (s *Server) handleDeleteSessionPreview(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	preview, _, _, _, err := s.collectSessionArtifacts(r.Context(), sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleExportSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	_, sourceFiles, transcriptRel, historyLines, err := s.collectSessionArtifacts(r.Context(), sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Strings(sourceFiles)
	var transcript [][]byte
	for _, filePath := range sourceFiles {
		_, matched, err := partitionSessionFile(filePath, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		transcript = append(transcript, matched...)
	}
	if len(transcript) == 0 {
		http.Error(w, "no session lines found on disk", http.StatusNotFound)
		return
	}
	if transcriptRel == "" {
		transcriptRel = filepath.ToSlash(filepath.Join("unknown-project", sessionID+".jsonl"))
	}

	sessionEnvPath := s.sessionEnvPath(sessionID)
	fileHistoryPath := s.fileHistoryPath(sessionID)
	manifest := sessionBundleManifest{
		Kind:                "session",
		Version:             1,
		SessionID:           sessionID,
		ProjectSlug:         splitProjectSlug(transcriptRel),
		TranscriptRelPath:   transcriptRel,
		ExportedAt:          time.Now().UTC(),
		TranscriptLines:     len(transcript),
		HistoryLines:        len(historyLines),
		IncludesSessionEnv:  pathExists(sessionEnvPath),
		IncludesFileHistory: pathExists(fileHistoryPath),
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tarAddBytes(tw, "manifest.json", 0o644, manifestJSON); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tarAddBytes(tw, "transcript/session.jsonl", 0o644, bytes.Join(transcript, nil)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(historyLines) > 0 {
		if err := tarAddBytes(tw, "history/history.jsonl", 0o644, bytes.Join(historyLines, nil)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if manifest.IncludesSessionEnv {
		if err := tarAddTree(tw, sessionEnvPath, "session-env"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if manifest.IncludesFileHistory {
		if err := tarAddTree(tw, fileHistoryPath, "file-history"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := gw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filename := sanitizeSessionFilename(strings.TrimSpace(r.URL.Query().Get("filename")))
	if filename == "" {
		filename = sanitizeSessionFilename(sessionID)
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".tgz") {
		filename += ".tgz"
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleExportAllSessions(w http.ResponseWriter, r *http.Request) {
	projectFiles, err := countProjectFiles(s.Scanner.Root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if projectFiles == 0 {
		http.Error(w, "no session files found on disk", http.StatusNotFound)
		return
	}
	manifest := sessionBundleManifest{
		Kind:                "all-sessions",
		Version:             1,
		ExportedAt:          time.Now().UTC(),
		ProjectFiles:        projectFiles,
		IncludesSessionEnv:  pathExists(filepath.Join(s.claudeDataRoot(), "session-env")),
		IncludesFileHistory: pathExists(filepath.Join(s.claudeDataRoot(), "file-history")),
	}
	if historyData, err := os.ReadFile(s.historyPath()); err == nil {
		manifest.HistoryLines, _ = countLinesAndSessions(historyData)
	} else if !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tarAddBytes(tw, "manifest.json", 0o644, manifestJSON); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tarAddTreeFiltered(tw, s.Scanner.Root, "projects", shouldExportProjectFile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if historyData, err := os.ReadFile(s.historyPath()); err == nil && len(historyData) > 0 {
		if err := tarAddBytes(tw, "history/history.jsonl", 0o644, historyData); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err != nil && !os.IsNotExist(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if manifest.IncludesSessionEnv {
		if err := tarAddTree(tw, filepath.Join(s.claudeDataRoot(), "session-env"), "session-env"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if manifest.IncludesFileHistory {
		if err := tarAddTree(tw, filepath.Join(s.claudeDataRoot(), "file-history"), "file-history"); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := tw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := gw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stamp := time.Now().Format("2006-01-02_1504")
	filename := sanitizeSessionFilename(strings.TrimSpace(r.URL.Query().Get("filename")))
	if filename == "" {
		filename = fmt.Sprintf("amatoken-all-sessions-%s.tgz", stamp)
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".tgz") {
		filename += ".tgz"
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	if sessionID == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}
	preview, sourceFiles, _, historyLines, err := s.collectSessionArtifacts(r.Context(), sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
		Backup  bool   `json:"backup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Confirm != "DELETE" {
		http.Error(w, "confirmation token must be DELETE", http.StatusBadRequest)
		return
	}

	type fileResult struct {
		SourceFile   string `json:"source_file"`
		Kind         string `json:"kind,omitempty"`
		BackupPath   string `json:"backup_path,omitempty"`
		RemovedLines int    `json:"removed_lines"`
	}
	results := make([]fileResult, 0, len(preview.Files))
	stamp := currentStamp()
	totalRemoved := 0

	for _, filePath := range sourceFiles {
		kept, matched, err := partitionSessionFile(filePath, sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(matched) == 0 {
			continue
		}
		backupPath, err := backupAndRewriteSessionFile(filePath, kept, stamp, body.Backup)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		results = append(results, fileResult{
			SourceFile:   filePath,
			Kind:         "transcript",
			BackupPath:   backupPath,
			RemovedLines: len(matched),
		})
		totalRemoved += len(matched)
	}

	if len(historyLines) > 0 {
		kept, matched, err := partitionSessionFile(s.historyPath(), sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(matched) > 0 {
			backupPath, err := backupAndRewriteSessionFile(s.historyPath(), kept, stamp, body.Backup)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			results = append(results, fileResult{
				SourceFile:   s.historyPath(),
				Kind:         "history",
				BackupPath:   backupPath,
				RemovedLines: len(matched),
			})
			totalRemoved += len(matched)
		}
	}

	for _, artifactPath := range []struct {
		kind string
		path string
	}{
		{kind: "session-env", path: s.sessionEnvPath(sessionID)},
		{kind: "file-history", path: s.fileHistoryPath(sessionID)},
	} {
		backupPath, moved, err := backupExistingPath(artifactPath.path, stamp, body.Backup)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if moved {
			results = append(results, fileResult{
				SourceFile: artifactPath.path,
				Kind:       artifactPath.kind,
				BackupPath: backupPath,
			})
		}
	}

	if len(results) == 0 {
		http.Error(w, "no session artifacts found on disk", http.StatusNotFound)
		return
	}
	if err := s.Repo.DeleteRecordsBySourceFiles(r.Context(), sourceFiles); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Repo.DeleteIngestStateBySourceFiles(r.Context(), sourceFiles); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, filePath := range sourceFiles {
		if err := s.Scanner.ProcessFile(r.Context(), filePath); err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":    sessionID,
		"removed_lines": totalRemoved,
		"file_count":    len(results),
		"files":         results,
		"backup":        body.Backup,
	})
}

func (s *Server) handleImportSession(w http.ResponseWriter, r *http.Request) {
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 256<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		http.Error(w, "empty file", http.StatusBadRequest)
		return
	}

	if bundle, err := parseAllSessionsBundle(data); err == nil {
		stamp := currentStamp()
		projectFiles := 0
		for _, entry := range bundle.Projects {
			dest, err := safeJoin(s.Scanner.Root, entry.Path)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if _, err := writeFileReplacing(dest, entry.Data, 0o644, stamp); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if strings.HasSuffix(strings.ToLower(dest), ".jsonl") {
				if err := s.Repo.DeleteRecordsBySourceFiles(r.Context(), []string{dest}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := s.Repo.DeleteIngestStateBySourceFiles(r.Context(), []string{dest}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := s.Scanner.ProcessFile(r.Context(), dest); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				projectFiles++
			}
		}
		historyBackup := ""
		if len(bundle.History) > 0 {
			historyBackup, err = writeFileReplacing(s.historyPath(), bundle.History, 0o644, stamp)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		sessionEnvBackup, sessionEnvFiles, err := writeBundleTree(filepath.Join(s.claudeDataRoot(), "session-env"), bundle.SessionEnv, stamp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fileHistoryBackup, fileHistoryFiles, err := writeBundleTree(filepath.Join(s.claudeDataRoot(), "file-history"), bundle.FileHistory, stamp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessionIDs := collectImportedSessionIDs(bundle.Projects)
		writeJSON(w, http.StatusOK, map[string]any{
			"bundle":              true,
			"full_bundle":         true,
			"project_files":       projectFiles,
			"line_count":          countImportedLines(bundle.Projects),
			"session_ids":         sessionIDs,
			"history_lines_added": bundle.Manifest.HistoryLines,
			"history_backup":      historyBackup,
			"session_env_files":   sessionEnvFiles,
			"file_history_files":  fileHistoryFiles,
			"session_env_backup":  sessionEnvBackup,
			"file_history_backup": fileHistoryBackup,
			"original_filename":   hdr.Filename,
		})
		return
	}

	if bundle, err := parseSessionBundle(data); err == nil {
		dest, err := safeJoin(s.Scanner.Root, bundle.Manifest.TranscriptRelPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		stamp := currentStamp()
		transcriptBackup, err := writeFileReplacing(dest, bundle.Transcript, 0o644, stamp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.Repo.DeleteRecordsBySourceFiles(r.Context(), []string{dest}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.Repo.DeleteIngestStateBySourceFiles(r.Context(), []string{dest}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		importedHistoryLines, err := mergeSessionLinesFile(s.historyPath(), bundle.Manifest.SessionID, bundle.History, 0o644)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessionEnvBackup, sessionEnvFiles, err := writeBundleTree(s.sessionEnvPath(bundle.Manifest.SessionID), bundle.SessionEnv, stamp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fileHistoryBackup, fileHistoryFiles, err := writeBundleTree(s.fileHistoryPath(bundle.Manifest.SessionID), bundle.FileHistory, stamp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.Scanner.ProcessFile(r.Context(), dest); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		lineCount, sessionIDs := countLinesAndSessions(bundle.Transcript)
		writeJSON(w, http.StatusOK, map[string]any{
			"bundle":              true,
			"imported_file":       dest,
			"line_count":          lineCount,
			"session_ids":         sessionIDs,
			"history_lines_added": importedHistoryLines,
			"session_env_files":   sessionEnvFiles,
			"file_history_files":  fileHistoryFiles,
			"transcript_backup":   transcriptBackup,
			"session_env_backup":  sessionEnvBackup,
			"file_history_backup": fileHistoryBackup,
			"original_filename":   hdr.Filename,
			"transcript_rel_path": bundle.Manifest.TranscriptRelPath,
			"project_slug":        bundle.Manifest.ProjectSlug,
		})
		return
	}

	lineCount, sessionIDs := countLinesAndSessions(data)
	if lineCount == 0 {
		http.Error(w, "no JSONL lines found", http.StatusBadRequest)
		return
	}
	importsDir := filepath.Join(s.Scanner.Root, "_amatoken_imports")
	if err := os.MkdirAll(importsDir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	base := strings.TrimSuffix(filepath.Base(hdr.Filename), filepath.Ext(hdr.Filename))
	base = sanitizeSessionFilename(base)
	if base == "" {
		base = "session-import"
	}
	stamp := currentStamp()
	dest := filepath.Join(importsDir, fmt.Sprintf("%s-%s.jsonl", base, stamp))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Repo.DeleteRecordsBySourceFiles(r.Context(), []string{dest}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Repo.DeleteIngestStateBySourceFiles(r.Context(), []string{dest}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Scanner.ProcessFile(r.Context(), dest); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bundle":        false,
		"imported_file": dest,
		"line_count":    lineCount,
		"session_ids":   sessionIDs,
		"original_file": hdr.Filename,
	})
}
