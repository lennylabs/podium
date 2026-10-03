package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/revmark"
)

// cacheCmd dispatches `podium cache <subcommand>`.
//
//	podium cache prune [--days N] [--dir DIR] [--dry-run]
//	podium cache reset-revisions [--dir DIR] [<artifact-id>...]
//
// The §6.5 content cache holds content_hash-keyed buckets that are
// immutable forever. `prune` removes buckets older than --days
// since their last access, matching common content-addressed-cache
// hygiene. The default is 30 days.
func cacheCmd(args []string) int {
	if len(args) < 1 || isHelpArg(args[0]) {
		printGroupHelp("cache", "Manage the local content cache.", [][2]string{
			{"prune", "Remove content-cache buckets older than N days."},
			{"reset-revisions", "Delete the revision marks podium-mcp keeps for latest loads."},
		})
		if len(args) < 1 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "prune":
		return cachePrune(args[1:])
	case "reset-revisions":
		return cacheResetRevisions(args[1:])
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
	cacheDir, err := resolveCacheDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	*dir = cacheDir
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
		// §6.5: the resolution index (revmark.DirName, which also holds the
		// revision marks) and any other dot-prefixed bookkeeping directory
		// are not content buckets; never prune them, or offline resolution
		// loses its (id, version) index and podium-mcp its revision marks.
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
	// A dry run deletes nothing, so its summary reports what a real run would
	// prune and keep rather than claiming buckets were pruned.
	if *dryRun {
		fmt.Printf("cache: would prune %d bucket(s) (%d B), would keep %d (cutoff %s)\n",
			pruned, bytesPruned, kept, cutoff.Format(time.RFC3339))
		return 0
	}
	fmt.Printf("cache: pruned %d bucket(s) (%d B), kept %d (cutoff %s)\n",
		pruned, bytesPruned, kept, cutoff.Format(time.RFC3339))
	return 0
}

// resolveCacheDir returns dir, or the default ~/.podium/cache when dir is
// empty. The --dir flag already defaults to PODIUM_CACHE_DIR, so an empty
// value means neither was set.
func resolveCacheDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".podium", "cache"), nil
}

// cacheResetRevisions deletes the §6.5 revision marks podium-mcp persists in
// the resolution index under the cache directory. Positional arguments name
// artifact IDs and match across every registry; with none, every mark is
// deleted. The reset reaches only persisted marks: a podium-mcp that holds the
// index keeps it locked, so the command fails rather than racing it, and a
// podium-mcp that ran on in-memory marks clears them only when it exits.
//
// Spec: §6.5
func cacheResetRevisions(args []string) int {
	fs := flag.NewFlagSet("cache reset-revisions", flag.ContinueOnError)
	setUsage(fs, "Delete the revision marks podium-mcp keeps for latest loads.")
	dir := fs.String("dir", os.Getenv("PODIUM_CACHE_DIR"), "cache directory (defaults to ~/.podium/cache)")
	fs.SetOutput(os.Stderr)
	ids, err := parseOperands(fs, args)
	if err != nil {
		return parseExit(err)
	}
	cacheDir, err := resolveCacheDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	deleted, found, err := revmark.Reset(context.Background(), cacheDir, ids)
	if errors.Is(err, revmark.ErrIndexLocked) {
		fmt.Fprintf(os.Stderr, "error: %s is in use by a running podium-mcp; stop every MCP server that uses this cache directory and retry. Marks a podium-mcp holds in memory are cleared when it exits.\n", revmark.IndexPath(cacheDir))
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if !found {
		fmt.Printf("cache: no revision marks under %s\n", cacheDir)
		return 0
	}
	fmt.Printf("cache: reset %d revision mark(s)\n", deleted)
	return 0
}

// bucketAccessTime returns the most recent mtime of any file
// inside the bucket directory. Falls back to the bucket dir's
// own mtime when empty. The MCP server touches bucket files on every
// cache hit (§6.5), so the newest mtime reflects last access
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
