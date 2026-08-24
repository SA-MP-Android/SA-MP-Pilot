package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/service"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
)

type pluginControllerStub struct{}

func (pluginControllerStub) List() []plugin.Info { return []plugin.Info{} }
func (pluginControllerStub) Debug(context.Context, string, string, string) (plugin.DebugResult, error) {
	return plugin.DebugResult{}, nil
}

func pluginHandler(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("app")}}
	return New(service.New(st), fs.FS(assets), slog.Default(), pluginControllerStub{})
}

func handler(t *testing.T) http.Handler {
	t.Helper()
	st, e := store.Open(filepath.Join(t.TempDir(), "d.json"))
	if e != nil {
		t.Fatal(e)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("app")}}
	return New(service.New(st), fs.FS(assets), slog.Default())
}
func TestCreateListDelete(t *testing.T) {
	h := handler(t)
	body := `{"host":"127.0.0.1","port":7777,"nickname":"web","password":"","encoding":"utf-8","autoConnect":false}`
	r := httptest.NewRequest("POST", "/api/instances", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("create %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"commands":[]`)) {
		t.Fatalf("create response must contain an empty commands array: %s", w.Body.String())
	}
	var created struct {
		Server struct {
			ID string `json:"id"`
		} `json:"server"`
	}
	if e := json.NewDecoder(w.Body).Decode(&created); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/api/instances", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("127.0.0.1")) {
		t.Fatalf("list: %s", w.Body.String())
	}
	r = httptest.NewRequest("DELETE", "/api/instances/"+created.Server.ID, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestRefreshGPCI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	initial := st.GPCI()
	h := New(service.New(st), fstest.MapFS{"index.html": {Data: []byte("app")}}, slog.Default())
	r := httptest.NewRequest(http.MethodPost, "/api/settings/gpci/refresh", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		GPCI string `json:"gpci"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.GPCI == "" || response.GPCI == initial || st.GPCI() != response.GPCI {
		t.Fatalf("unexpected refreshed GPCI: initial=%q response=%q stored=%q", initial, response.GPCI, st.GPCI())
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	h := handler(t)
	r := httptest.NewRequest("POST", "/api/instances", strings.NewReader(`{"unknown":true}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestRejectsOversizedPluginDebugCode(t *testing.T) {
	h := pluginHandler(t)
	body := `{"code":"` + strings.Repeat("x", plugin.MaxDebugCodeBytes+1) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/plugins/demo/debug", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestChatEndpointReturnsAnEmptyPage(t *testing.T) {
	h := handler(t)
	body := `{"host":"127.0.0.1","port":7777,"nickname":"web","password":"","encoding":"utf-8","autoConnect":false}`
	r := httptest.NewRequest(http.MethodPost, "/api/instances", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var created struct {
		Server struct {
			ID string `json:"id"`
		} `json:"server"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/instances/"+created.Server.ID+"/chat?limit=50", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("chat %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAndCommandLifecycle(t *testing.T) {
	h := handler(t)
	createBody := `{"host":"127.0.0.1","port":7777,"nickname":"web","password":"","encoding":"utf-8","autoConnect":false}`
	r := httptest.NewRequest(http.MethodPost, "/api/instances", strings.NewReader(createBody))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var created struct {
		Server struct {
			ID string `json:"id"`
		} `json:"server"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id := created.Server.ID
	updateBody := `{"host":"localhost","port":7778,"nickname":"updated","password":"secret","encoding":"gbk","autoConnect":true}`
	r = httptest.NewRequest(http.MethodPut, "/api/instances/"+id, strings.NewReader(updateBody))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("updated")) {
		t.Fatalf("update %d: %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodPost, "/api/instances/"+id+"/commands", strings.NewReader(`{"label":"example","command":"/example argument"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("command create %d: %s", w.Code, w.Body.String())
	}
	var command struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&command); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/instances/"+id+"/commands/"+command.ID, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("command delete %d: %s", w.Code, w.Body.String())
	}
}
