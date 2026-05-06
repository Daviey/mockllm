package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Daviey/mockllm/internal/generate"
)

func main() {
	source := flag.String("source", "https://models.dev/api.json", "URL or file path to models.dev api.json")
	output := flag.String("output", "./providers", "output directory for generated specs")
	providers := flag.String("providers", "", "comma-separated list of provider IDs to generate (default: all)")
	flag.Parse()

	initLogger()

	slog.Info("fetching models.dev data", "source", *source)

	var data map[string]generate.ModelsDevProvider
	var err error

	if strings.HasPrefix(*source, "http") {
		data, err = generate.FetchAPI(*source)
	} else {
		data, err = generate.LoadAPI(*source)
	}
	if err != nil {
		slog.Error("failed to load data", "error", err)
		os.Exit(1)
	}

	slog.Info("data loaded", "providers", len(data))

	var include []string
	if *providers != "" {
		include = strings.Split(*providers, ",")
	}

	specs := generate.GenerateAll(data, include)
	slog.Info("specs generated", "count", len(specs))

	if err := generate.WriteSpecs(*output, specs); err != nil {
		slog.Error("failed to write specs", "error", err)
		os.Exit(1)
	}

	for _, spec := range specs {
		fmt.Printf("  %s\n", spec.Filename)
	}

	slog.Info("done", "output", *output)
}

func initLogger() {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
}
