package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"filtrite/internal/ruleset"
)

func main() {
	input := flag.String("input", "filters/adblock.txt", "legacy-compatible filter-list")
	output := flag.String("output", "dist/adblock.dat", "unindexed Chromium ruleset")
	converter := flag.String("converter", "deps/ruleset_converter", "ruleset_converter executable")
	logPath := flag.String("log", "build/work/adblock/ruleset-converter.log", "converter log")
	timeout := flag.Duration("timeout", 5*time.Minute, "converter timeout")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if err := ruleset.Convert(ctx, ruleset.Options{Converter: *converter, Input: *input, Output: *output, Log: *logPath}); err != nil {
		log.Fatal(err)
	}
	log.Printf("generated %s (%s)", filepath.Clean(*output), fileSize(*output))
}
func fileSize(p string) string {
	if s, e := os.Stat(p); e == nil {
		return formatBytes(s.Size())
	}
	return "unknown"
}
func formatBytes(n int64) string {
	const m = 1024 * 1024
	if n >= m {
		return fmt.Sprintf("%.2f MiB", float64(n)/float64(m))
	}
	return fmt.Sprintf("%d bytes", n)
}
