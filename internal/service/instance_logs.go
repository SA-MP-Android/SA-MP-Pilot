package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
)

const (
	instanceLogExtension = ".log"
	instanceLogDirMode   = 0o700
	instanceLogFileMode  = 0o600
	defaultChatPageSize  = 50
	maxChatPageSize      = 100
	logReadBlockSize     = 4096
)

var safeLogID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type instanceLogEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Text      string    `json:"text"`
	Color     string    `json:"color"`
}

func (m *Manager) instanceLogPath(id string) string {
	name := id
	if !safeLogID.MatchString(name) {
		digest := sha256.Sum256([]byte(id))
		name = hex.EncodeToString(digest[:])
	}
	return filepath.Join(m.logDir, name+instanceLogExtension)
}

func (m *Manager) resetInstanceLog(id string) error {
	if m.logDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.logDir, instanceLogDirMode); err != nil {
		return err
	}
	file, err := os.OpenFile(m.instanceLogPath(id), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, instanceLogFileMode)
	if err != nil {
		return err
	}
	return file.Close()
}

func (m *Manager) writeInstanceLog(id string, message domain.ChatMessage) {
	if m.logDir == "" {
		return
	}
	if err := os.MkdirAll(m.logDir, instanceLogDirMode); err != nil {
		return
	}
	line, err := json.Marshal(instanceLogEntry{ID: message.ID, Timestamp: message.At, Text: message.Text, Color: message.Color})
	if err != nil {
		return
	}
	line = append(line, '\n')
	file, err := os.OpenFile(m.instanceLogPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, instanceLogFileMode)
	if err != nil {
		return
	}
	_, _ = file.Write(line)
	_ = file.Close()
}

func (m *Manager) Chat(id string, before int64, limit int) (domain.ChatPage, error) {
	if _, ok := m.find(id); !ok {
		return domain.ChatPage{}, errors.New("instance not found")
	}
	if m.logDir == "" {
		return domain.ChatPage{Items: []domain.ChatMessage{}}, nil
	}
	if limit <= 0 {
		limit = defaultChatPageSize
	}
	if limit > maxChatPageSize {
		limit = maxChatPageSize
	}
	file, err := os.Open(m.instanceLogPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return domain.ChatPage{Items: []domain.ChatMessage{}}, nil
	}
	if err != nil {
		return domain.ChatPage{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return domain.ChatPage{}, err
	}
	end := before
	if end <= 0 || end > stat.Size() {
		end = stat.Size()
	}
	lines, next, err := readLogLinesBackward(file, end, limit)
	if err != nil {
		return domain.ChatPage{}, err
	}
	items := make([]domain.ChatMessage, 0, len(lines))
	for index := len(lines) - 1; index >= 0; index-- {
		var entry instanceLogEntry
		if json.Unmarshal(lines[index], &entry) == nil {
			items = append(items, domain.ChatMessage{ID: entry.ID, Text: entry.Text, Color: entry.Color, At: entry.Timestamp})
		}
	}
	return domain.ChatPage{Items: items, NextBefore: next}, nil
}

func readLogLinesBackward(file *os.File, end int64, limit int) ([][]byte, int64, error) {
	lines := make([][]byte, 0, limit)
	position := end
	base := end
	var buffered []byte
	oldest := end
	for position > 0 && len(lines) < limit {
		start := position - logReadBlockSize
		if start < 0 {
			start = 0
		}
		block := make([]byte, position-start)
		if _, err := file.ReadAt(block, start); err != nil {
			return nil, 0, err
		}
		buffered = append(block, buffered...)
		base = start
		for len(lines) < limit {
			buffered = bytes.TrimSuffix(buffered, []byte{'\n'})
			separator := bytes.LastIndexByte(buffered, '\n')
			if separator < 0 {
				break
			}
			lineStart := separator + 1
			lines = append(lines, bytes.Clone(buffered[lineStart:]))
			oldest = base + int64(lineStart)
			buffered = buffered[:separator]
		}
		position = start
	}
	if position == 0 && len(buffered) > 0 && len(lines) < limit {
		lines = append(lines, bytes.Clone(buffered))
		oldest = 0
	}
	if oldest == 0 {
		return lines, 0, nil
	}
	return lines, oldest, nil
}
