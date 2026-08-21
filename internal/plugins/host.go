package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
)

const (
	manifestFile                = "plugin.json"
	pluginWatchInterval         = time.Second
	pluginReadyTimeout          = 10 * time.Second
	pluginAPICallTimeout        = 15 * time.Second
	pluginDebugTimeout          = 15 * time.Second
	pluginMessageQueueCapacity  = 256
	pluginLogCapacity           = 500
	pluginMaxLogMessageBytes    = 16 * 1024
	pluginMaxMessageBytes       = 1 << 20
	pluginMaxSubscriptionCount  = 256
	pluginMaxSubscriptionBytes  = 256
	pluginMaxConcurrentAPICalls = 32
	pluginObserverQueueCapacity = 256
	pluginRestartInitialDelay   = time.Second
	pluginRestartMaxDelay       = 30 * time.Second
	nodeModulesDir              = "node_modules"
)

var (
	errPluginStopped      = errors.New("plugin stopped")
	errPluginNotRunning   = errors.New("plugin is not running")
	errPluginMessageLarge = errors.New("plugin protocol message is too large")
	errPluginProtocol     = errors.New("invalid plugin protocol message")
	errPluginReadyTimeout = fmt.Errorf("plugin did not send ready within %s", pluginReadyTimeout)
	errPluginTooManyCalls = errors.New("too many concurrent plugin API calls")
)

type Host struct {
	dir           string
	api           plugin.Invoker
	log           *slog.Logger
	mu            sync.RWMutex
	processes     map[string]*process
	closed        atomic.Bool
	lifecycle     sync.Mutex
	observerMu    sync.RWMutex
	observer      func(plugin.Event)
	observerQueue chan plugin.Event
	observerStop  chan struct{}
	observerOnce  sync.Once
	watchStop     chan struct{}
	watchOnce     sync.Once
}

type process struct {
	host             *Host
	manifest         plugin.Manifest
	command          *exec.Cmd
	stdin            io.WriteCloser
	out              *messageQueue
	done             chan struct{}
	finish           sync.Once
	mu               sync.RWMutex
	status           string
	errText          string
	dir              string
	events           []string
	logs             []plugin.Log
	pending          map[uint64]chan plugin.Message
	ready            chan struct{}
	readyOnce        sync.Once
	debugSlot        chan struct{}
	stopRequested    atomic.Bool
	apiSlots         chan struct{}
	nextID           atomic.Uint64
	sent             atomic.Uint64
	dropped          atomic.Uint64
	restartAttempt   int
	restartScheduled atomic.Bool
}

type messageQueue struct {
	mu          sync.Mutex
	items       []plugin.Message
	wake        chan struct{}
	space       chan struct{}
	closed      chan struct{}
	closedState bool
	once        sync.Once
	capacity    int
}

func newMessageQueue(capacity int) *messageQueue {
	return &messageQueue{
		wake: make(chan struct{}, 1), space: make(chan struct{}, 1),
		closed: make(chan struct{}), capacity: capacity,
	}
}

func (q *messageQueue) tryPush(message plugin.Message) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closedState || len(q.items) >= q.capacity {
		return false
	}
	q.items = append(q.items, message)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

func (q *messageQueue) pushContext(ctx context.Context, message plugin.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		q.mu.Lock()
		if q.closedState {
			q.mu.Unlock()
			return errPluginStopped
		}
		if len(q.items) < q.capacity {
			q.items = append(q.items, message)
			q.mu.Unlock()
			select {
			case q.wake <- struct{}{}:
			default:
			}
			return nil
		}
		q.mu.Unlock()
		select {
		case <-q.space:
		case <-q.closed:
			return errPluginStopped
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (q *messageQueue) pop(done <-chan struct{}) (plugin.Message, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			message := q.items[0]
			q.items[0] = plugin.Message{}
			q.items = q.items[1:]
			if len(q.items) == 0 {
				q.items = nil
			}
			q.mu.Unlock()
			select {
			case q.space <- struct{}{}:
			default:
			}
			return message, true
		}
		q.mu.Unlock()
		select {
		case <-q.wake:
		case <-done:
			return plugin.Message{}, false
		}
	}
}

func (q *messageQueue) close() {
	q.once.Do(func() {
		q.mu.Lock()
		q.closedState = true
		q.items = nil
		q.mu.Unlock()
		close(q.closed)
	})
}

func New(dir string, api plugin.Invoker, logger *slog.Logger) (*Host, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if dir == "" {
		dir = "plugins"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	h := &Host{
		dir: dir, api: api, log: logger, processes: map[string]*process{},
		observerQueue: make(chan plugin.Event, pluginObserverQueueCapacity),
		observerStop:  make(chan struct{}),
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := h.load(filepath.Join(dir, entry.Name())); err != nil {
			logger.Error("load plugin", "path", entry.Name(), "error", err)
		}
	}
	h.watchStop = make(chan struct{})
	go h.observerLoop()
	go h.watchLoop()
	return h, nil
}

func (h *Host) load(pluginDir string) error {
	b, err := os.ReadFile(filepath.Join(pluginDir, manifestFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var manifest plugin.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return fmt.Errorf("decode %s: %w", manifestFile, err)
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	p := h.newProcess(manifest, pluginDir)
	return h.addProcess(p)
}

func (h *Host) addProcess(p *process) error {
	manifest := p.manifest
	pluginDir := p.dir
	h.mu.Lock()
	if _, exists := h.processes[manifest.ID]; exists {
		h.mu.Unlock()
		return fmt.Errorf("duplicate plugin id %q", manifest.ID)
	}
	h.processes[manifest.ID] = p
	h.mu.Unlock()
	if manifest.Enabled != nil && !*manifest.Enabled {
		p.mu.Lock()
		p.status = plugin.StatusDisabled
		p.mu.Unlock()
		return nil
	}
	if err := p.start(pluginDir); err != nil {
		p.setError(err)
		return err
	}
	return nil
}

func (h *Host) newProcess(manifest plugin.Manifest, pluginDir string) *process {
	return &process{
		host: h, manifest: manifest, dir: pluginDir,
		out: newMessageQueue(pluginMessageQueueCapacity), done: make(chan struct{}),
		status: plugin.StatusStopped, pending: map[uint64]chan plugin.Message{},
		ready: make(chan struct{}), debugSlot: make(chan struct{}, 1),
		apiSlots: make(chan struct{}, pluginMaxConcurrentAPICalls),
		events:   normalizeSubscriptions(manifest.Events),
	}
}

func validateManifest(manifest plugin.Manifest) error {
	if manifest.ID == "" || len(manifest.ID) > 128 || strings.ContainsAny(manifest.ID, `/\\`) {
		return errors.New("plugin id must be a non-empty path-safe string")
	}
	if manifest.Command == "" {
		return errors.New("plugin command is required")
	}
	if len(manifest.Events) > pluginMaxSubscriptionCount {
		return fmt.Errorf("too many plugin subscriptions: maximum is %d", pluginMaxSubscriptionCount)
	}
	for _, event := range manifest.Events {
		if len(event) > pluginMaxSubscriptionBytes {
			return fmt.Errorf("plugin subscription is too large: maximum is %d bytes", pluginMaxSubscriptionBytes)
		}
		if !validSubscriptionPattern(strings.TrimSpace(event)) {
			return fmt.Errorf("invalid plugin subscription pattern %q", event)
		}
	}
	return nil
}

func (h *Host) processFor(id string) (*process, error) {
	h.mu.RLock()
	p := h.processes[id]
	h.mu.RUnlock()
	if p == nil {
		return nil, errors.New("plugin not found")
	}
	return p, nil
}

// Start starts a stopped plugin. A running plugin is left untouched.
func (h *Host) Start(id string) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.closed.Load() {
		return errors.New("plugin host is closed")
	}
	p, err := h.processFor(id)
	if err != nil {
		return err
	}
	p.mu.RLock()
	running := p.status == plugin.StatusRunning || p.status == plugin.StatusStarting
	manifest, dir := p.manifest, p.dir
	p.mu.RUnlock()
	if running {
		return nil
	}
	return h.replaceProcess(id, p, manifest, dir)
}

// Stop terminates a plugin process and keeps its manifest available for a
// later Start or Restart.
func (h *Host) Stop(id string) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	p, err := h.processFor(id)
	if err != nil {
		return err
	}
	p.stop()
	h.notifyStatus(p)
	return nil
}

// Restart reloads plugin.json and starts a fresh process. This is also the
// operation used by the file watcher after source changes.
func (h *Host) Restart(id string) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	return h.restartLocked(id)
}

func (h *Host) replaceProcess(id string, old *process, manifest plugin.Manifest, dir string) error {
	old.stop()
	p := h.newProcess(manifest, dir)
	old.mu.RLock()
	p.logs = append(p.logs, old.logs...)
	if len(p.logs) > pluginLogCapacity {
		p.logs = append([]plugin.Log(nil), p.logs[len(p.logs)-pluginLogCapacity:]...)
	}
	p.sent.Store(old.sent.Load())
	p.dropped.Store(old.dropped.Load())
	p.restartAttempt = old.restartAttempt
	old.mu.RUnlock()
	if manifest.Enabled != nil && !*manifest.Enabled {
		p.mu.Lock()
		p.status = plugin.StatusDisabled
		p.mu.Unlock()
		h.mu.Lock()
		h.processes[id] = p
		h.mu.Unlock()
		h.notifyStatus(p)
		return nil
	}
	if err := p.start(dir); err != nil {
		p.setError(err)
		h.mu.Lock()
		h.processes[id] = p
		h.mu.Unlock()
		h.notifyStatus(p)
		return err
	}
	h.mu.Lock()
	h.processes[id] = p
	h.mu.Unlock()
	h.notifyStatus(p)
	return nil
}

func readManifest(dir string) (plugin.Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return plugin.Manifest{}, err
	}
	var manifest plugin.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return plugin.Manifest{}, fmt.Errorf("decode %s: %w", manifestFile, err)
	}
	if err := validateManifest(manifest); err != nil {
		return plugin.Manifest{}, err
	}
	return manifest, nil
}

func (p *process) start(pluginDir string) error {
	command := p.manifest.Command
	if !filepath.IsAbs(command) && strings.ContainsRune(command, filepath.Separator) {
		command = filepath.Join(pluginDir, command)
	}
	cmd := exec.Command(command, p.manifest.Args...)
	cmd.Dir = pluginDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	p.command, p.stdin = cmd, stdin
	p.mu.Lock()
	p.status, p.errText = plugin.StatusStarting, ""
	p.mu.Unlock()
	go p.writeLoop()
	go p.readLoop(stdout)
	go p.stderrLoop(stderr)
	go func() {
		err := cmd.Wait()
		p.finishWith(err)
	}()
	if !p.enqueue(plugin.Message{Type: plugin.MessageHello, Protocol: plugin.ProtocolVersion, Name: p.manifest.ID}) {
		p.fail(errors.New("failed to queue plugin handshake"))
		return errors.New("failed to queue plugin handshake")
	}
	p.host.notifyStatus(p)
	go p.awaitReady()
	return nil
}

func (p *process) awaitReady() {
	timer := time.NewTimer(pluginReadyTimeout)
	defer timer.Stop()
	select {
	case <-p.ready:
	case <-p.done:
	case <-timer.C:
		p.fail(errPluginReadyTimeout)
	}
}

func (p *process) stop() {
	p.stopRequested.Store(true)
	p.mu.RLock()
	command := p.command
	done := p.done
	p.mu.RUnlock()
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		<-done
	}
	p.mu.Lock()
	p.status = plugin.StatusStopped
	p.errText = ""
	p.mu.Unlock()
}

func (p *process) writeLoop() {
	for {
		message, ok := p.out.pop(p.done)
		if !ok {
			return
		}
		encoded, err := marshalProtocolMessage(message)
		if err != nil {
			p.fail(err)
			return
		}
		if written, err := p.stdin.Write(encoded); err != nil {
			p.fail(err)
			return
		} else if written != len(encoded) {
			p.fail(io.ErrShortWrite)
			return
		}
	}
}

func marshalProtocolMessage(message plugin.Message) ([]byte, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(encoded)+1 > pluginMaxMessageBytes {
		return nil, errPluginMessageLarge
	}
	return append(encoded, '\n'), nil
}

func (p *process) readLoop(reader io.Reader) {
	buffered := bufio.NewReaderSize(reader, pluginMaxMessageBytes)
	for {
		line, err := readProtocolLine(buffered)
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return
		}
		if err != nil {
			p.fail(fmt.Errorf("%w: %v", errPluginProtocol, err))
			return
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var message plugin.Message
		if err := json.Unmarshal(line, &message); err != nil {
			p.fail(fmt.Errorf("%w: %v", errPluginProtocol, err))
			return
		}
		if err := validateMessage(message); err != nil {
			p.fail(err)
			return
		}
		p.handle(message)
	}
}

func readProtocolLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, pluginMaxMessageBytes)
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > pluginMaxMessageBytes {
			return nil, errPluginMessageLarge
		}
		line = append(line, part...)
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return line, nil
		}
		return line, err
	}
}

func validateMessage(message plugin.Message) error {
	switch message.Type {
	case plugin.MessageReady, plugin.MessageSubscribe, plugin.MessageCall, plugin.MessageLog, plugin.MessageResult, plugin.MessageDebugResult:
	default:
		return fmt.Errorf("%w: unsupported message type %q", errPluginProtocol, message.Type)
	}
	if len(message.Data) > pluginMaxMessageBytes || len(message.Params) > pluginMaxMessageBytes {
		return errPluginMessageLarge
	}
	if len(message.Code) > pluginMaxMessageBytes {
		return errPluginMessageLarge
	}
	if len(message.Message) > pluginMaxLogMessageBytes {
		return fmt.Errorf("%w: log message is too large", errPluginMessageLarge)
	}
	if message.Events != nil {
		if len(*message.Events) > pluginMaxSubscriptionCount {
			return fmt.Errorf("%w: too many subscriptions", errPluginProtocol)
		}
		for _, event := range *message.Events {
			if len(event) > pluginMaxSubscriptionBytes {
				return fmt.Errorf("%w: subscription is too large", errPluginProtocol)
			}
			if !validSubscriptionPattern(strings.TrimSpace(event)) {
				return fmt.Errorf("%w: invalid subscription pattern %q", errPluginProtocol, event)
			}
		}
	}
	if message.Type == plugin.MessageSubscribe && message.Events == nil {
		return fmt.Errorf("%w: subscribe requires an events array", errPluginProtocol)
	}
	if message.Type == plugin.MessageCall && message.Method == "" {
		return fmt.Errorf("%w: API method is required", errPluginProtocol)
	}
	if message.Type == plugin.MessageReady && message.Protocol != plugin.ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %d", errPluginProtocol, message.Protocol)
	}
	return nil
}

func (p *process) stderrLoop(reader io.Reader) {
	buffered := bufio.NewReader(reader)
	for {
		line, err := readBoundedLogLine(buffered, pluginMaxLogMessageBytes)
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line != "" {
			p.addLog("stderr", line)
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			p.fail(err)
			return
		}
	}
}

func readBoundedLogLine(reader *bufio.Reader, maxBytes int) (string, error) {
	line := make([]byte, 0, maxBytes)
	truncated := false
	for {
		part, isPrefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return string(line), err
		}
		if !truncated {
			remaining := maxBytes - len(line)
			if len(part) > remaining {
				line = append(line, part[:remaining]...)
				truncated = true
			} else {
				line = append(line, part...)
			}
		}
		if !isPrefix {
			return string(line), nil
		}
	}
}

func (p *process) handle(message plugin.Message) {
	switch message.Type {
	case plugin.MessageReady:
		ready := false
		p.mu.Lock()
		if p.status == plugin.StatusStarting {
			p.status = plugin.StatusRunning
			ready = true
		}
		if message.Events != nil {
			p.events = normalizeRuntimeSubscriptions(*message.Events)
		}
		p.mu.Unlock()
		if ready {
			p.mu.Lock()
			p.restartAttempt = 0
			p.mu.Unlock()
			p.readyOnce.Do(func() { close(p.ready) })
			p.host.notifyStatus(p)
		}
	case plugin.MessageSubscribe:
		p.mu.Lock()
		if message.Events == nil {
			p.events = normalizeRuntimeSubscriptions(nil)
		} else {
			p.events = normalizeRuntimeSubscriptions(*message.Events)
		}
		p.mu.Unlock()
	case plugin.MessageCall:
		select {
		case p.apiSlots <- struct{}{}:
			go func() {
				defer func() { <-p.apiSlots }()
				p.call(message)
			}()
		default:
			if !p.enqueue(plugin.Message{Type: plugin.MessageResult, ID: message.ID, Error: errPluginTooManyCalls.Error()}) {
				p.addLog("warn", errPluginTooManyCalls.Error())
			}
		}
	case plugin.MessageLog:
		level := message.Level
		if level == "" {
			level = "info"
		}
		p.addLog(level, message.Message)
	case plugin.MessageResult, plugin.MessageDebugResult:
		p.mu.Lock()
		waiter := p.pending[message.ID]
		delete(p.pending, message.ID)
		p.mu.Unlock()
		if waiter != nil {
			waiter <- message
		}
	default:
		p.addLog("warn", "unknown message type: "+message.Type)
	}
}

func (p *process) call(message plugin.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginAPICallTimeout)
	defer cancel()
	var result any
	var err error
	if p.host.api == nil {
		err = errors.New("plugin API is not configured")
	} else {
		result, err = p.host.api.InvokePluginAPI(ctx, message.InstanceID, message.Method, message.Params)
	}
	if enqueueErr := p.enqueueContext(ctx, plugin.Message{Type: plugin.MessageResult, ID: message.ID, Result: result, Error: errorText(err)}); enqueueErr != nil && p.host.log != nil {
		p.host.log.Warn("plugin API result was not delivered", "id", p.manifest.ID, "error", enqueueErr)
	}
}

func (p *process) request(ctx context.Context, message plugin.Message) (plugin.Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	message.ID = p.nextID.Add(1)
	waiter := make(chan plugin.Message, 1)
	p.mu.Lock()
	if p.status != plugin.StatusRunning {
		p.mu.Unlock()
		return plugin.Message{}, errPluginNotRunning
	}
	p.pending[message.ID] = waiter
	p.mu.Unlock()
	if err := p.enqueueContext(ctx, message); err != nil {
		p.mu.Lock()
		delete(p.pending, message.ID)
		p.mu.Unlock()
		return plugin.Message{}, err
	}
	select {
	case response := <-waiter:
		if response.Error != "" {
			return response, errors.New(response.Error)
		}
		return response, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, message.ID)
		p.mu.Unlock()
		return plugin.Message{}, ctx.Err()
	case <-p.done:
		return plugin.Message{}, errPluginStopped
	}
}

func (p *process) enqueue(message plugin.Message) bool {
	return p.out.tryPush(message)
}

func (p *process) enqueueContext(ctx context.Context, message plugin.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errPluginStopped
	default:
		return p.out.pushContext(ctx, message)
	}
}

func (p *process) finishWith(err error) {
	shouldRestart := false
	p.finish.Do(func() {
		close(p.done)
		p.out.close()
		p.mu.Lock()
		wasActive := p.status == plugin.StatusRunning || p.status == plugin.StatusStarting || p.status == plugin.StatusError
		if p.stopRequested.Load() {
			p.status, p.errText = plugin.StatusStopped, ""
		} else if err != nil && p.status != plugin.StatusError {
			p.status, p.errText = plugin.StatusError, err.Error()
		} else if err == nil && (p.status == plugin.StatusRunning || p.status == plugin.StatusStarting) {
			p.status = plugin.StatusStopped
		}
		restartEnabled := p.manifest.Restart == nil || *p.manifest.Restart
		shouldRestart = wasActive && restartEnabled && !p.stopRequested.Load() && !p.host.closed.Load()
		for id, waiter := range p.pending {
			waiter <- plugin.Message{ID: id, Type: plugin.MessageResult, Error: errPluginStopped.Error()}
			delete(p.pending, id)
		}
		p.mu.Unlock()
		p.host.notifyStatus(p)
		if shouldRestart && p.restartScheduled.CompareAndSwap(false, true) {
			go p.host.scheduleRestart(p)
		}
	})
}

func (h *Host) scheduleRestart(old *process) {
	old.mu.Lock()
	old.restartAttempt++
	attempt := old.restartAttempt
	old.mu.Unlock()
	delay := pluginRestartInitialDelay
	for index := 1; index < attempt; index++ {
		if delay >= pluginRestartMaxDelay/2 {
			delay = pluginRestartMaxDelay
			break
		}
		delay *= 2
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-h.watchStop:
		return
	}
	if h.closed.Load() || old.stopRequested.Load() {
		return
	}
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	current, err := h.processFor(old.manifest.ID)
	if err != nil || current != old || h.closed.Load() || old.stopRequested.Load() {
		return
	}
	manifest, err := readManifest(old.dir)
	if err != nil || manifest.ID != old.manifest.ID {
		if err != nil {
			old.setError(err)
		}
		return
	}
	if err := h.replaceProcess(old.manifest.ID, old, manifest, old.dir); err != nil && h.log != nil {
		h.log.Error("auto-restart plugin", "id", old.manifest.ID, "error", err)
	}
}

func (p *process) setError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.errText = err.Error()
	if p.status == plugin.StatusRunning || p.status == plugin.StatusStarting {
		p.status = plugin.StatusError
	}
	p.mu.Unlock()
	p.host.notifyStatus(p)
}

func (p *process) fail(err error) {
	if err == nil {
		return
	}
	p.setError(err)
	p.mu.RLock()
	command := p.command
	p.mu.RUnlock()
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
	}
}

func (p *process) addLog(level, message string) {
	if message == "" {
		return
	}
	entry := plugin.Log{At: time.Now(), Level: level, Message: truncate(message, pluginMaxLogMessageBytes)}
	p.mu.Lock()
	if len(p.logs) == pluginLogCapacity {
		copy(p.logs, p.logs[1:])
		p.logs[len(p.logs)-1] = entry
	} else {
		p.logs = append(p.logs, entry)
	}
	p.mu.Unlock()
	if p.host.log != nil {
		p.host.log.Info("plugin", "id", p.manifest.ID, "level", level, "message", entry.Message)
	}
	p.host.notify(plugin.Event{
		Name: plugin.EventPluginLog,
		Data: map[string]any{"pluginId": p.manifest.ID, "at": entry.At, "level": entry.Level, "message": entry.Message},
	})
}

func truncate(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func (p *process) info() plugin.Info {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return plugin.Info{
		Manifest: p.manifest, Status: p.status, Error: p.errText,
		EventsSent: p.sent.Load(), EventsDropped: p.dropped.Load(), Logs: append([]plugin.Log(nil), p.logs...),
	}
}

func (h *Host) SetObserver(observer func(plugin.Event)) {
	h.observerMu.Lock()
	h.observer = observer
	h.observerMu.Unlock()
}

func (h *Host) notify(event plugin.Event) {
	select {
	case h.observerQueue <- event:
	case <-h.observerStop:
	default:
	}
}

func (h *Host) observerLoop() {
	for {
		select {
		case <-h.observerStop:
			return
		case event := <-h.observerQueue:
			h.observerMu.RLock()
			observer := h.observer
			h.observerMu.RUnlock()
			if observer != nil {
				observer(event)
			}
		}
	}
}

func (h *Host) notifyStatus(p *process) {
	info := p.info()
	h.notify(plugin.Event{
		Name: plugin.EventPluginStatus,
		Data: map[string]any{
			"pluginId": info.Manifest.ID,
			"status":   info.Status,
			"error":    info.Error,
		},
	})
}

type fileStamp struct {
	Size     int64
	Modified int64
}

func (h *Host) directoryStates() map[string]map[string]fileStamp {
	h.mu.RLock()
	dirs := make(map[string]string, len(h.processes))
	for id, p := range h.processes {
		p.mu.RLock()
		dirs[id] = p.dir
		p.mu.RUnlock()
	}
	h.mu.RUnlock()
	states := make(map[string]map[string]fileStamp, len(dirs))
	for id, dir := range dirs {
		state := map[string]fileStamp{}
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == nodeModulesDir {
					return filepath.SkipDir
				}
				return nil
			}
			info, statErr := entry.Info()
			if statErr == nil {
				state[path] = fileStamp{Size: info.Size(), Modified: info.ModTime().UnixNano()}
			}
			return nil
		})
		states[id] = state
	}
	return states
}

func (h *Host) watchLoop() {
	previous := h.directoryStates()
	ticker := time.NewTicker(pluginWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.watchStop:
			return
		case <-ticker.C:
			h.lifecycle.Lock()
			h.discoverLocked()
			current := h.directoryStates()
			for id, state := range current {
				if _, existed := previous[id]; !existed {
					continue
				}
				if reflect.DeepEqual(previous[id], state) {
					continue
				}
				if err := h.restartLocked(id); err != nil && h.log != nil {
					h.log.Error("reload plugin", "id", id, "error", err)
				}
			}
			previous = current
			h.lifecycle.Unlock()
		}
	}
}

func (h *Host) restartLocked(id string) error {
	if h.closed.Load() {
		return errors.New("plugin host is closed")
	}
	p, err := h.processFor(id)
	if err != nil {
		return err
	}
	p.mu.RLock()
	dir := p.dir
	p.mu.RUnlock()
	manifest, err := readManifest(dir)
	if err != nil {
		p.setError(err)
		h.notifyStatus(p)
		return err
	}
	if manifest.ID != id {
		return errors.New("plugin id cannot change during reload")
	}
	return h.replaceProcess(id, p, manifest, dir)
}

func (h *Host) discoverLocked() {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		if h.log != nil {
			h.log.Error("scan plugin directory", "path", h.dir, "error", err)
		}
		return
	}
	seenDirectories := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(h.dir, entry.Name())
		seenDirectories[dir] = struct{}{}
		manifest, manifestErr := readManifest(dir)
		if manifestErr != nil {
			continue
		}
		h.mu.RLock()
		_, exists := h.processes[manifest.ID]
		h.mu.RUnlock()
		if exists {
			continue
		}
		if err := h.addProcess(h.newProcess(manifest, dir)); err != nil && h.log != nil {
			h.log.Error("load plugin", "path", entry.Name(), "error", err)
		}
	}

	h.mu.RLock()
	removed := make([]*process, 0)
	for _, p := range h.processes {
		p.mu.RLock()
		dir := p.dir
		p.mu.RUnlock()
		if _, exists := seenDirectories[dir]; !exists {
			removed = append(removed, p)
		}
	}
	h.mu.RUnlock()
	for _, p := range removed {
		h.mu.Lock()
		if current, exists := h.processes[p.manifest.ID]; exists && current == p {
			delete(h.processes, p.manifest.ID)
		}
		h.mu.Unlock()
		p.stop()
		h.notifyStatus(p)
	}
}

// Emit sends an event to every matching plugin. Events are deliberately
// lossy: a plugin that cannot keep up must not stall the network loop.
func (h *Host) Emit(event plugin.Event) {
	h.mu.RLock()
	processes := make([]*process, 0, len(h.processes))
	for _, p := range h.processes {
		processes = append(processes, p)
	}
	h.mu.RUnlock()
	var data json.RawMessage
	if event.Data != nil {
		var err error
		data, err = json.Marshal(event.Data)
		if err != nil {
			data = json.RawMessage(fmt.Sprintf(`{"error":%q}`, err.Error()))
		}
	}
	eventTime := event.Time
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	delivered := make([]string, 0, len(processes))
	for _, p := range processes {
		p.mu.RLock()
		running := p.status == plugin.StatusRunning
		matches := matchesEvent(p.events, event.Name)
		p.mu.RUnlock()
		if !running || !matches {
			continue
		}
		if p.enqueue(plugin.Message{Type: plugin.MessageEvent, Name: event.Name, InstanceID: event.InstanceID, Time: &eventTime, Data: data}) {
			p.sent.Add(1)
			delivered = append(delivered, p.manifest.ID)
		} else {
			p.dropped.Add(1)
		}
	}
	for _, id := range delivered {
		h.notify(plugin.Event{Name: plugin.EventPluginEvent, Data: map[string]any{"pluginId": id, "event": event}})
	}
}

func (h *Host) List() []plugin.Info {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]plugin.Info, 0, len(h.processes))
	for _, p := range h.processes {
		result = append(result, p.info())
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Manifest.ID < result[right].Manifest.ID })
	return result
}

func (h *Host) Debug(ctx context.Context, id, instanceID, code string) (plugin.DebugResult, error) {
	h.mu.RLock()
	p := h.processes[id]
	h.mu.RUnlock()
	if p == nil {
		return plugin.DebugResult{}, errors.New("plugin not found")
	}
	if len(code) > plugin.MaxDebugCodeBytes {
		return plugin.DebugResult{}, errPluginMessageLarge
	}
	if ctx == nil {
		ctx = context.Background()
	}
	debugContext, cancel := context.WithTimeout(ctx, pluginDebugTimeout)
	defer cancel()
	select {
	case p.debugSlot <- struct{}{}:
		defer func() { <-p.debugSlot }()
	case <-debugContext.Done():
		return plugin.DebugResult{}, debugContext.Err()
	case <-p.done:
		return plugin.DebugResult{}, errPluginStopped
	}
	response, err := p.request(debugContext, plugin.Message{Type: plugin.MessageDebug, InstanceID: instanceID, Code: code})
	if err != nil {
		return plugin.DebugResult{}, err
	}
	return plugin.DebugResult{PluginID: id, InstanceID: instanceID, Result: response.Result}, nil
}

func (h *Host) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.watchOnce.Do(func() {
		if h.watchStop != nil {
			close(h.watchStop)
		}
	})
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	h.mu.RLock()
	processes := make([]*process, 0, len(h.processes))
	for _, p := range h.processes {
		processes = append(processes, p)
	}
	h.mu.RUnlock()
	for _, p := range processes {
		p.stop()
	}
	h.observerOnce.Do(func() { close(h.observerStop) })
}

func normalizeSubscriptions(events []string) []string {
	if len(events) == 0 {
		return []string{"*"}
	}
	return normalizeRuntimeSubscriptions(events)
}

func normalizeRuntimeSubscriptions(events []string) []string {
	result := make([]string, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" {
			continue
		}
		if _, exists := seen[event]; exists {
			continue
		}
		seen[event] = struct{}{}
		result = append(result, event)
	}
	return result
}

func validSubscriptionPattern(pattern string) bool {
	if pattern == "" || pattern == "*" {
		return pattern == "*"
	}
	wildcard := strings.IndexByte(pattern, '*')
	return wildcard < 0 || wildcard == len(pattern)-1
}

func matchesEvent(subscriptions []string, event string) bool {
	for _, subscription := range subscriptions {
		if subscription == "*" || subscription == event {
			return true
		}
		if strings.HasSuffix(subscription, "*") && strings.HasPrefix(event, strings.TrimSuffix(subscription, "*")) {
			return true
		}
	}
	return false
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
