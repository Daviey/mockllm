package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Daviey/mockllm/internal/provider"
	"github.com/Daviey/mockllm/internal/server"
)

func main() {
	port := flag.Int("port", 8080, "port to listen on")
	specsDir := flag.String("specs", "", "path to provider specs directory (default: ./providers)")
	debug := flag.Bool("debug", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(server.Version)
		os.Exit(0)
	}

	initLogger(*debug)

	dir := resolveSpecsDir(*specsDir)
	slog.Info("loading specs", "dir", dir)

	registry := provider.NewRegistry()
	if err := registry.LoadDir(dir); err != nil {
		slog.Error("error loading specs", "error", err)
		os.Exit(1)
	}

	slog.Info("specs loaded", "providers", registry.Providers())

	srv := server.New(registry, *port)
	if err := srv.Start(); err != nil {
		slog.Error("error starting server", "error", err)
		os.Exit(1)
	}
}

func initLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

func resolveSpecsDir(flag string) string {
	if flag != "" {
		return flag
	}

	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "providers")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	return "./providers"
}
