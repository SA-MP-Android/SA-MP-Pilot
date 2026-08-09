package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SA-MP-Android/SA-MP-Pilot/internal/httpapi"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/service"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/store"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	data := flag.String("data", "", "data directory")
	web := flag.String("web", "", "frontend directory")
	flag.Parse()
	runtimeDir, e := executableDirectory()
	if e != nil {
		slog.Error("executable dir", "error", e)
		os.Exit(1)
	}
	if *data == "" {
		*data = runtimeDir
	}
	if *web == "" {
		*web = filepath.Join(runtimeDir, "web", "dist")
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
	manager.StartAutoConnect()
	defer manager.Close()
	srv := &http.Server{Addr: *addr, Handler: httpapi.New(manager, os.DirFS(*web), log), ReadHeaderTimeout: 5 * time.Second}
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
