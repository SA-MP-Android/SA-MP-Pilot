package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
)

func TestMatchesEventSupportsExactPrefixAndAll(t *testing.T) {
	tests := []struct {
		subscriptions []string
		event         string
		want          bool
	}{
		{[]string{"client.chat"}, "client.chat", true},
		{[]string{"client.*"}, "client.player.join", true},
		{[]string{"instance.*"}, "chat.message", false},
		{[]string{"*"}, "anything.new", true},
	}
	for _, test := range tests {
		if got := matchesEvent(test.subscriptions, test.event); got != test.want {
			t.Fatalf("matchesEvent(%v, %q) = %v, want %v", test.subscriptions, test.event, got, test.want)
		}
	}
}

func TestHostLifecycleAndHotReload(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(plugin.Manifest{ID: "demo", Name: "Demo", Version: "1", Command: "node", Args: []string{"main.cjs"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	writePlugin := func(result string) {
		script := `const readline = require('node:readline')
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity })
process.stdout.write(JSON.stringify({ type: 'ready', events: ['*'] }) + '\n')
rl.on('line', (line) => {
  const message = JSON.parse(line)
  if (message.type === 'debug') process.stdout.write(JSON.stringify({ type: 'debug.result', id: message.id, result: '` + result + `' }) + '\n')
})
`
		if err := os.WriteFile(filepath.Join(dir, "main.cjs"), []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePlugin("v1")
	host, err := New(root, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	observed := make(chan plugin.Event, 32)
	host.SetObserver(func(event plugin.Event) { observed <- event })
	waitRunning := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			items := host.List()
			if len(items) == 1 && items[0].Status == plugin.StatusRunning {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("plugin did not start: %+v", host.List())
	}
	waitRunning()
	debug := func() plugin.DebugResult {
		t.Helper()
		result, debugErr := host.Debug(context.Background(), "demo", "", "return true")
		if debugErr != nil {
			t.Fatal(debugErr)
		}
		return result
	}
	if got := debug().Result; got != "v1" {
		t.Fatalf("initial debug result = %v", got)
	}
	host.Emit(plugin.Event{Name: plugin.EventClientPrefix + "chat", InstanceID: "instance-1", Data: map[string]string{"text": "hello"}})
	eventDeadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-observed:
			if event.Name == plugin.EventPluginEvent {
				goto eventObserved
			}
		case <-eventDeadline:
			t.Fatal("plugin event was not observed")
		}
	}

eventObserved:
	if err := host.Stop("demo"); err != nil {
		t.Fatal(err)
	}
	if host.List()[0].Status != plugin.StatusStopped {
		t.Fatalf("status after stop = %q", host.List()[0].Status)
	}
	if err := host.Start("demo"); err != nil {
		t.Fatal(err)
	}
	waitRunning()
	writePlugin("v2-reloaded")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if host.List()[0].Status == plugin.StatusRunning {
			result, debugErr := host.Debug(context.Background(), "demo", "", "return true")
			if debugErr == nil && result.Result == "v2-reloaded" {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("hot reload did not update debug result: %+v", host.List())
}

func TestNormalizeSubscriptionsDefaultsToAll(t *testing.T) {
	if got := normalizeSubscriptions(nil); len(got) != 1 || got[0] != "*" {
		t.Fatalf("default subscriptions = %v", got)
	}
	if got := normalizeSubscriptions([]string{" ", "client.chat"}); len(got) != 1 || got[0] != "client.chat" {
		t.Fatalf("normalized subscriptions = %v", got)
	}
}

func TestValidateManifest(t *testing.T) {
	if err := validateManifest(plugin.Manifest{ID: "demo", Command: "node"}); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range []plugin.Manifest{
		{Command: "node"},
		{ID: "../demo", Command: "node"},
		{ID: "demo"},
	} {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("validateManifest(%+v) unexpectedly succeeded", manifest)
		}
	}
}

func TestMessageQueueIsBoundedAndContextAware(t *testing.T) {
	queue := newMessageQueue(1)
	first := plugin.Message{Type: plugin.MessageEvent, Name: "first"}
	second := plugin.Message{Type: plugin.MessageEvent, Name: "second"}
	if !queue.tryPush(first) {
		t.Fatal("first message was not queued")
	}
	if queue.tryPush(second) {
		t.Fatal("queue accepted a message beyond its capacity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := queue.pushContext(ctx, second); err == nil {
		t.Fatal("full queue push unexpectedly succeeded")
	}
	if got, ok := queue.pop(make(chan struct{})); !ok || got.Name != first.Name {
		t.Fatalf("popped message = %+v, ok=%v", got, ok)
	}
	if err := queue.pushContext(context.Background(), second); err != nil {
		t.Fatalf("queue did not accept a message after pop: %v", err)
	}
	queue.close()
	if queue.tryPush(first) {
		t.Fatal("closed queue accepted a message")
	}
}

func TestOutboundProtocolMessageIsBounded(t *testing.T) {
	largeData := json.RawMessage(`"` + strings.Repeat("x", pluginMaxMessageBytes) + `"`)
	if _, err := marshalProtocolMessage(plugin.Message{Type: plugin.MessageLog, Data: largeData}); err == nil {
		t.Fatal("oversized outbound message unexpectedly succeeded")
	}
}

func TestPluginStderrLinesAreBounded(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(strings.Repeat("x", pluginMaxLogMessageBytes*2) + "\nnext\n"))
	line, err := readBoundedLogLine(reader, pluginMaxLogMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(line) != pluginMaxLogMessageBytes {
		t.Fatalf("bounded line length = %d, want %d", len(line), pluginMaxLogMessageBytes)
	}
	next, err := readBoundedLogLine(reader, pluginMaxLogMessageBytes)
	if err != nil || next != "next" {
		t.Fatalf("next log line = %q, error = %v", next, err)
	}
}

func TestPluginLogsAreBounded(t *testing.T) {
	host := &Host{processes: map[string]*process{}}
	p := host.newProcess(plugin.Manifest{ID: "logs", Command: "node"}, t.TempDir())
	for index := 0; index < pluginLogCapacity+10; index++ {
		p.addLog("info", "entry-"+fmt.Sprint(index))
	}
	info := p.info()
	if len(info.Logs) != pluginLogCapacity {
		t.Fatalf("log count = %d, want %d", len(info.Logs), pluginLogCapacity)
	}
	if info.Logs[0].Message != "entry-10" {
		t.Fatalf("oldest retained log = %q, want entry-10", info.Logs[0].Message)
	}
}

func TestNormalizeRuntimeSubscriptionsAllowsEmptySet(t *testing.T) {
	if got := normalizeRuntimeSubscriptions(nil); len(got) != 0 {
		t.Fatalf("empty runtime subscriptions = %v, want empty", got)
	}
}

func TestHostDiscoversAndRemovesDisabledPlugins(t *testing.T) {
	root := t.TempDir()
	host, err := New(root, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	dir := filepath.Join(root, "disabled")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(plugin.Manifest{ID: "disabled", Command: "node", Enabled: boolPtr(false)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool { return len(host.List()) == 1 })
	if got := host.List()[0].Status; got != plugin.StatusDisabled {
		t.Fatalf("discovered plugin status = %q, want %q", got, plugin.StatusDisabled)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool { return len(host.List()) == 0 })
}

func TestHostStopsPluginWithInvalidProtocol(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "invalid")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(plugin.Manifest{ID: "invalid", Command: "node", Args: []string{"main.cjs"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.cjs"), []byte("process.stdout.write('not-json\\n')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := New(root, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	waitUntil(t, 3*time.Second, func() bool { return host.List()[0].Status == plugin.StatusError })
}

func TestJavaScriptDebugExecutionHasTimeout(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	sdkPath, err := filepath.Abs(filepath.Join(filepath.Dir(sourceFile), "../../examples/plugins/auto-spawn/sdk.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "debug")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(plugin.Manifest{ID: "debug", Command: "node", Args: []string{"main.cjs"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "const { pathToFileURL } = require('node:url')\nimport(pathToFileURL(" + fmt.Sprintf("%q", sdkPath) + ").href).then(({ start }) => start())\n"
	if err := os.WriteFile(filepath.Join(dir, "main.cjs"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := New(root, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	waitUntil(t, 3*time.Second, func() bool { return host.List()[0].Status == plugin.StatusRunning })
	started := time.Now()
	_, err = host.Debug(context.Background(), "debug", "", "while (true) {}")
	if err == nil {
		t.Fatal("infinite debug script unexpectedly succeeded")
	}
	if time.Since(started) > pluginDebugTimeout+time.Second {
		t.Fatalf("debug timeout took too long: %s", time.Since(started))
	}
}

func boolPtr(value bool) *bool { return &value }

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
