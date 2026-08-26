package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/httpapi"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/plugins"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/service"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/webassets"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	authUser := flag.String("auth-user", os.Getenv("SAMP_PILOT_AUTH_USER"), "HTTP Basic Auth username (overrides SAMP_PILOT_AUTH_USER)")
	authPassword := flag.String("auth-password", os.Getenv("SAMP_PILOT_AUTH_PASSWORD"), "HTTP Basic Auth password (overrides SAMP_PILOT_AUTH_PASSWORD)")
	insecure := flag.Bool("insecure", false, "disable HTTP Basic Auth and allow non-loopback listen addresses (unsafe)")
	data := flag.String("data", "", "data directory")
	web := flag.String("web", "", "frontend directory")
	pluginDir := flag.String("plugins", "", "plugin directory")
	flag.Parse()
	if (*authUser == "") != (*authPassword == "") {
		slog.Error("HTTP Basic Auth requires both -auth-user and -auth-password")
		os.Exit(1)
	}
	auth := httpapi.BasicAuthConfig{Username: *authUser, Password: *authPassword}
	if *insecure {
		auth = httpapi.BasicAuthConfig{}
	}
	if err := validateListenSecurity(*addr, *insecure, auth.Enabled()); err != nil {
		slog.Error("refusing HTTP listen configuration", "address", *addr, "error", err)
		os.Exit(1)
	}
	runtimeDir, e := executableDirectory()
	if e != nil {
		slog.Error("executable dir", "error", e)
		os.Exit(1)
	}
	if *data == "" {
		*data = runtimeDir
	}
	st, e := store.Open(filepath.Join(*data, "data.json"))
	if e != nil {
		slog.Error("open store", "error", e)
		os.Exit(1)
	}
	logDir := filepath.Join(*data, "logs")
	if e = os.MkdirAll(logDir, 0o700); e != nil {
		slog.Error("create log dir", "error", e)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	manager := service.New(st, service.WithLogDir(logDir))
	if *pluginDir == "" {
		*pluginDir = filepath.Join(*data, "plugins")
	}
	pluginHost, e := plugins.New(*pluginDir, manager, log)
	if e != nil {
		log.Error("open plugin host", "error", e)
		os.Exit(1)
	}
	manager.SetPluginSink(pluginHost)
	pluginHost.SetObserver(manager.PublishPluginEvent)
	defer pluginHost.Close()
	manager.StartAutoConnect()
	defer manager.Close()
	var assets fs.FS = webassets.FS
	if *web != "" {
		assets = os.DirFS(*web)
	}
	if *insecure {
		log.Warn("HTTP Basic Auth is disabled; the HTTP server is unauthenticated", "address", *addr)
	}
	srv := &http.Server{Addr: *addr, Handler: httpapi.NewWithAuth(manager, assets, log, auth, pluginHost), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("SA-MP-Pilot listening", "version", version, "address", "http://"+*addr)
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Error("server", "error", e)
			os.Exit(1)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}

func validateListenSecurity(address string, insecure, authEnabled bool) error {
	if insecure || authEnabled || isLoopbackListenAddress(address) {
		return nil
	}
	return fmt.Errorf("non-loopback addresses require HTTP Basic Auth or -insecure")
}

func isLoopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func executableDirectory() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}
