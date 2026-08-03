package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runIncidentCreate(args []string) {
	fs := flag.NewFlagSet("incident create", flag.ExitOnError)
	journalPath := fs.String("journal", "", "NDJSON journal path")
	outPath := fs.String("out", "", "output tar.gz path")
	claim := fs.String("claim", "", "optional claim filter")
	service := fs.String("service", "unknown", "service label (sanitized)")
	_ = fs.Parse(args)
	if *journalPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: shiftlock-inspect incident create -journal PATH -out FILE.tar.gz")
		os.Exit(2)
	}
	entries, err := loadJournalEntries(*journalPath, *claim)
	if err != nil {
		fatal(err)
	}
	// sanitize attrs again
	for i := range entries {
		if entries[i].Attrs == nil {
			continue
		}
		clean := map[string]string{}
		for k, v := range entries[i].Attrs {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "password") || strings.Contains(lk, "secret") ||
				strings.Contains(lk, "authorization") || lk == "token" {
				continue
			}
			clean[k] = v
		}
		entries[i].Attrs = clean
	}
	explain := explainEntries(*claim, entries)
	meta := map[string]any{
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"service":    sanitizeLabel(*service),
		"claim":      *claim,
		"tool":       "shiftlock-inspect",
		"note":       "sanitized incident bundle; no secrets included by design",
	}

	if err := writeIncidentTar(*outPath, meta, entries, explain); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote sanitized incident bundle %s (%d journal entries)\n", *outPath, len(entries))
}

func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return s
}

func writeIncidentTar(path string, meta map[string]any, entries any, explain any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		// ignore when dir is .
		if filepath.Dir(path) != "" && filepath.Dir(path) != "." {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	addJSON := func(name string, v any) error {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		b = append(b, '\n')
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(b)
		return err
	}
	if err := addJSON("meta.json", meta); err != nil {
		return err
	}
	if err := addJSON("journal.json", entries); err != nil {
		return err
	}
	if err := addJSON("explain.json", explain); err != nil {
		return err
	}
	readme := []byte("ShiftLock sanitized incident bundle.\nContains journal + rule-based explain only.\nNo credentials.\n")
	hdr := &tar.Header{Name: "README.txt", Mode: 0o644, Size: int64(len(readme)), ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = tw.Write(readme)
	return err
}
