package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheCmd dispatches `podium cache <subcommand>`.
//
//	podium cache prune [--days N] [--dir DIR] [--dry-run]
//
// The §6.5 content cache holds content_hash-keyed buckets that are
// immutable forever. `prune` removes buckets older than --days
// since their last access, matching common content-addressed-cache
// hygiene. The default is 30 days.
func cacheCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("cache", "Manage the local content cache.", [][2]string{
			{"prune", "Remove content-cache buckets older than N days."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "prune":
		return cachePrune(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown cache subcommand: %s\n", args[0])
		return 2
	}
}

func cachePrune(args []string) int {
	fs := flag.NewFlagSet("cache prune", flag.ContinueOnError)
	setUsage(fs, "Remove content-cache buckets older than N days.")
	dir := fs.String("dir", os.Getenv("PODIUM_CACHE_DIR"), "cache directory (defaults to ~/.podium/cache)")
	days := fs.Int("days", 30, "remove buckets last accessed more than N days ago (0 = older than now)")
	dryRun := fs.Bool("dry-run", false, "report what would be removed; remove nothing")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		*dir = filepath.Join(home, ".podium", "cache")
	}
	// --days 0 is the boundary "older than now": the cutoff is the present
	// moment, so every bucket whose last access is in the past is prunable. This
	// is the timing-independent way to select the whole cache, used to confirm a
	// freshly-warmed bucket is prunable. A negative count would push the cutoff
	// into the future and evict buckets newer than now, which is nonsensical, so
	// reject it.
	if *days < 0 {
		fmt.Fprintln(os.Stderr, "error: --days must not be negative")
		return 2
	}
	cutoff := time.Now().Add(-time.Duration(*days) * 24 * time.Hour)

	entries, err := os.ReadDir(*dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("cache: %s does not exist; nothing to prune\n", *dir)
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	pruned := 0
	kept := 0
	bytesPruned := int64(0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// §6.5 (F-6.5.3): the resolution index (`.resolutions`) and any other
		// dot-prefixed bookkeeping directory are not content buckets; never
		// prune them, or offline resolution loses its (id, version) index.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		bucket := filepath.Join(*dir, e.Name())
		// A content bucket always holds a `frontmatter` file; skip any
		// directory that is not one so prune cannot delete unrelated data.
		if _, err := os.Stat(filepath.Join(bucket, "frontmatter")); err != nil {
			continue
		}
		mtime, size := bucketAccessTime(bucket)
		if !mtime.Before(cutoff) {
			kept++
			continue
		}
		bytesPruned += size
		if *dryRun {
			fmt.Printf("would prune: %s (last accessed %s)\n", e.Name(), mtime.Format(time.RFC3339))
			pruned++
			continue
		}
		if err := os.RemoveAll(bucket); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot remove %s: %v\n", bucket, err)
			continue
		}
		pruned++
	}
	fmt.Printf("cache: pruned %d bucket(s) (%d B), kept %d (cutoff %s)\n",
		pruned, bytesPruned, kept, cutoff.Format(time.RFC3339))
	return 0
}

// bucketAccessTime returns the most recent mtime of any file
// inside the bucket directory. Falls back to the bucket dir's
// own mtime when empty. The MCP server touches bucket files on every
// cache hit (§6.5, F-6.5.6), so the newest mtime reflects last access
// (read or write), not just the original write time.
func bucketAccessTime(bucket string) (time.Time, int64) {
	info, err := os.Stat(bucket)
	if err != nil {
		return time.Time{}, 0
	}
	latest := info.ModTime()
	totalSize := int64(0)
	_ = filepath.Walk(bucket, func(_ string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil || fi.IsDir() {
			return nil
		}
		if fi.ModTime().After(latest) {
			latest = fi.ModTime()
		}
		totalSize += fi.Size()
		return nil
	})
	return latest, totalSize
}
