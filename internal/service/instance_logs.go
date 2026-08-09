package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	instanceLogExtension = ".log"
	instanceLogDirMode   = 0o700
	instanceLogFileMode  = 0o600
)

var safeLogID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type instanceLogEntry struct {
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

func (m *Manager) writeInstanceLog(id, text, color string, timestamp time.Time) {
	if m.logDir == "" {
		return
	}
	if err := os.MkdirAll(m.logDir, instanceLogDirMode); err != nil {
		return
	}
	line, err := json.Marshal(instanceLogEntry{Timestamp: timestamp, Text: text, Color: color})
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
