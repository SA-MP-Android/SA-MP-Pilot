package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/domain"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/service"
	"github.com/SA-MP-Android/SA-MP-Pilot/plugin"
	"github.com/coder/websocket"
)

const (
	maxRequestBodyBytes int64 = 1 << 20
)

type Server struct {
	manager *service.Manager
	assets  fs.FS
	log     *slog.Logger
	plugins PluginController
}

type PluginController interface {
	List() []plugin.Info
	Debug(context.Context, string, string, string) (plugin.DebugResult, error)
}

func New(m *service.Manager, assets fs.FS, log *slog.Logger, controllers ...PluginController) http.Handler {
	var plugins PluginController
	if len(controllers) > 0 {
		plugins = controllers[0]
	}
	s := &Server{manager: m, assets: assets, log: log, plugins: plugins}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/plugins", s.pluginsList)
	mux.HandleFunc("POST /api/plugins/{id}/debug", s.pluginDebug)
	mux.HandleFunc("POST /api/plugins/{id}/start", s.pluginStart)
	mux.HandleFunc("POST /api/plugins/{id}/stop", s.pluginStop)
	mux.HandleFunc("POST /api/plugins/{id}/restart", s.pluginRestart)
	mux.HandleFunc("GET /api/instances", s.list)
	mux.HandleFunc("POST /api/instances", s.create)
	mux.HandleFunc("GET /api/instances/{id}", s.get)
	mux.HandleFunc("GET /api/instances/{id}/chat", s.chat)
	mux.HandleFunc("PUT /api/instances/{id}", s.update)
	mux.HandleFunc("DELETE /api/instances/{id}", s.remove)
	mux.HandleFunc("POST /api/instances/{id}/commands", s.addCommand)
	mux.HandleFunc("DELETE /api/instances/{id}/commands/{commandId}", s.deleteCommand)
	mux.HandleFunc("POST /api/instances/{id}/connect", s.connect)
	mux.HandleFunc("POST /api/instances/{id}/disconnect", s.disconnect)
	mux.HandleFunc("POST /api/instances/{id}/actions/{action}", s.action)
	mux.HandleFunc("GET /api/events", s.events)
	mux.Handle("/", spa(assets))
	return security(mux)
}

func (s *Server) pluginsList(w http.ResponseWriter, _ *http.Request) {
	if s.plugins == nil {
		write(w, http.StatusOK, []plugin.Info{})
		return
	}
	write(w, http.StatusOK, s.plugins.List())
}

func (s *Server) pluginDebug(w http.ResponseWriter, r *http.Request) {
	if s.plugins == nil {
		problem(w, http.StatusServiceUnavailable, "plugin host is not configured")
		return
	}
	var request struct {
		InstanceID string `json:"instanceId"`
		Code       string `json:"code"`
	}
	if err := decode(w, r, &request); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(request.Code) == "" {
		problem(w, http.StatusBadRequest, "code is required")
		return
	}
	if len(request.Code) > plugin.MaxDebugCodeBytes {
		problem(w, http.StatusRequestEntityTooLarge, "debug code is too large")
		return
	}
	result, err := s.plugins.Debug(r.Context(), r.PathValue("id"), request.InstanceID, request.Code)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not running") {
			status = http.StatusNotFound
		}
		problem(w, status, err.Error())
		return
	}
	write(w, http.StatusOK, result)
}

type PluginLifecycleController interface {
	Start(string) error
	Stop(string) error
	Restart(string) error
}

func (s *Server) pluginLifecycle(w http.ResponseWriter, r *http.Request, action func(PluginLifecycleController, string) error) {
	controller, ok := s.plugins.(PluginLifecycleController)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "plugin lifecycle is not configured")
		return
	}
	if err := action(controller, r.PathValue("id")); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		problem(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) pluginStart(w http.ResponseWriter, r *http.Request) {
	s.pluginLifecycle(w, r, func(controller PluginLifecycleController, id string) error { return controller.Start(id) })
}

func (s *Server) pluginStop(w http.ResponseWriter, r *http.Request) {
	s.pluginLifecycle(w, r, func(controller PluginLifecycleController, id string) error { return controller.Stop(id) })
}

func (s *Server) pluginRestart(w http.ResponseWriter, r *http.Request) {
	s.pluginLifecycle(w, r, func(controller PluginLifecycleController, id string) error { return controller.Restart(id) })
}
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	before, err := parseOptionalInt64(r.URL.Query().Get("before"))
	if err != nil {
		problem(w, http.StatusBadRequest, "invalid before cursor")
		return
	}
	limit, err := parseOptionalInt(r.URL.Query().Get("limit"))
	if err != nil {
		problem(w, http.StatusBadRequest, "invalid limit")
		return
	}
	page, err := s.manager.Chat(r.PathValue("id"), before, limit)
	if err != nil {
		problem(w, http.StatusNotFound, err.Error())
		return
	}
	write(w, http.StatusOK, page)
}

func parseOptionalInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.Atoi(value)
}

func parseOptionalInt64(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}
func (s *Server) list(w http.ResponseWriter, _ *http.Request) { write(w, 200, s.manager.List()) }
func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	v, ok := s.manager.Get(r.PathValue("id"))
	if !ok {
		problem(w, 404, "instance not found")
		return
	}
	write(w, 200, v)
}
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var v domain.Server
	if err := decode(w, r, &v); err != nil {
		problem(w, 400, err.Error())
		return
	}
	out, err := s.manager.Create(v)
	if err != nil {
		problem(w, 400, err.Error())
		return
	}
	write(w, 201, out)
}
func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var v domain.Server
	if err := decode(w, r, &v); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.manager.Update(r.PathValue("id"), v)
	if err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	write(w, http.StatusOK, out)
}
func (s *Server) addCommand(w http.ResponseWriter, r *http.Request) {
	var v domain.QuickCommand
	if err := decode(w, r, &v); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.manager.AddCommand(r.PathValue("id"), v)
	if err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	write(w, http.StatusCreated, out)
}
func (s *Server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.DeleteCommand(r.PathValue("id"), r.PathValue("commandId")); err != nil {
		problem(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Delete(r.PathValue("id")); err != nil {
		problem(w, 404, err.Error())
		return
	}
	w.WriteHeader(204)
}
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Connect(r.PathValue("id")); err != nil {
		problem(w, 404, err.Error())
		return
	}
	w.WriteHeader(202)
}
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Disconnect(r.PathValue("id")); err != nil {
		problem(w, 404, err.Error())
		return
	}
	w.WriteHeader(202)
}
func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	if err := decode(w, r, &p); err != nil {
		problem(w, 400, err.Error())
		return
	}
	if err := s.manager.Action(r.PathValue("id"), r.PathValue("action"), p); err != nil {
		problem(w, 400, err.Error())
		return
	}
	w.WriteHeader(202)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	stateEvents, chatEvents, done, err := s.manager.Subscribe()
	if err != nil {
		problem(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer done()
	pluginEvents, pluginDone, err := s.manager.SubscribePluginEvents()
	if err != nil {
		problem(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer pluginDone()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-stateEvents:
			if !ok {
				return
			}
			if err = c.Write(r.Context(), websocket.MessageText, mustJSON(e)); err != nil {
				return
			}
		case e, ok := <-chatEvents:
			if !ok {
				return
			}
			if err = c.Write(r.Context(), websocket.MessageText, mustJSON(e)); err != nil {
				return
			}
		case e, ok := <-pluginEvents:
			if !ok {
				return
			}
			if err = c.Write(r.Context(), websocket.MessageText, mustJSON(e)); err != nil {
				return
			}
		}
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
func spa(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(root, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", 503)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
}
