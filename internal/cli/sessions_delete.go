package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/session"
)

const (
	// sessionDeletePreviewRows caps how many matching session ids the text
	// report prints, so a dry run over thousands of sessions stays readable.
	sessionDeletePreviewRows = 5
)

// sessionDeleteReport is the machine-readable outcome shared by the text and
// JSON views. It mirrors sessionRecoveryReport's field style.
type sessionDeleteReport struct {
	Directories      int      `json:"directories"`
	Matched          int      `json:"matched"`
	Deleted          int      `json:"deleted"`
	Busy             int      `json:"busy"`
	Unknown          int      `json:"unknown"` // empty-filter candidates whose metadata could not be refreshed
	AlreadyDeleted   int      `json:"alreadyDeleted"`
	Errors           []string `json:"errors"`
	DryRun           bool     `json:"dryRun"`
	DeletedSessionID []string `json:"deletedSessionIds,omitempty"`
	BusySessionID    []string `json:"busySessionIds,omitempty"`
}

func sessionsDeleteCommand(args []string) int {
	name := "sessions delete"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var dirs stringListFlag
	all := fs.Bool("all", false, "match every canonical session")
	empty := fs.Bool("empty", false, "match sessions with no completed turns")
	olderThan := fs.String("older-than", "", "match sessions idle for this long (e.g. 24h, 7d)")
	apply := fs.Bool("apply", false, "delete matching sessions (default: dry run)")
	jsonOut := fs.Bool("json", false, "print the delete report as JSON")
	fs.Var(&dirs, "dir", "session directory to clean; repeat for multiple directories")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: reasonix sessions delete (--all|--empty|--older-than DUR)... [--dir PATH] [--apply] [--json]")
		return 2
	}
	if !*all && !*empty && strings.TrimSpace(*olderThan) == "" {
		fmt.Fprintln(os.Stderr, "reasonix sessions delete requires at least one of --all, --empty, or --older-than")
		return 2
	}
	cutoff, err := parseSessionDeleteAge(*olderThan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reasonix sessions delete:", err)
		return 2
	}
	if len(dirs) == 0 {
		dirs = append(dirs, resolveCLISessionDir())
	}

	report := sessionDeleteReport{Errors: []string{}, DryRun: !*apply}
	for _, raw := range dirs {
		dir := filepath.Clean(strings.TrimSpace(raw))
		if dir == "" || dir == "." {
			report.Errors = append(report.Errors, "empty session directory")
			continue
		}
		report.Directories++
		runSessionDeleteDirectory(context.Background(), dir, sessionDeleteFilters{
			empty: *empty, cutoff: cutoff,
		}, *apply, &report)
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	} else {
		printSessionDeleteReport(report, *apply)
	}
	for _, message := range report.Errors {
		fmt.Fprintln(os.Stderr, "warning:", message)
	}
	if len(report.Errors) > 0 {
		return 1
	}
	return 0
}

type sessionDeleteFilters struct {
	empty  bool
	cutoff time.Time // zero means no age filter
}

// runSessionDeleteDirectory walks one canonical store and deletes every
// session matching the filters, delegating the traversal and classification
// to the shared session.Prune so the CLI and web UI report identical numbers.
// It maps the legacy dir to its sibling sessions-v4 root the same way the
// serve and CLI surfaces do, so the command cleans exactly the sessions
// `reasonix serve` creates.
func runSessionDeleteDirectory(ctx context.Context, dir string, filters sessionDeleteFilters, apply bool, report *sessionDeleteReport) {
	root := sessionStoreRootForCleanup(dir)
	if root == "" {
		report.Errors = append(report.Errors, fmt.Sprintf("no canonical session store for %s", dir))
		return
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(root))
	if err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("open %s: %v", root, err))
		return
	}
	// Shutdown stops the cold-query metadata workers this command's own service
	// spawned, in addition to releasing any runtime. It is the lifecycle
	// boundary for a short-lived cleanup process.
	defer func() { _ = service.Shutdown(context.Background()) }()
	prune := service.Prune(ctx, session.PruneOptions{
		EmptyOnly: filters.empty,
		Cutoff:    filters.cutoff,
		DryRun:    !apply,
	})
	report.Matched += prune.Matched
	report.Deleted += prune.Deleted
	report.Busy += prune.Busy
	report.Unknown += prune.Unknown
	report.AlreadyDeleted += prune.AlreadyDeleted
	report.Errors = append(report.Errors, prune.Errors...)
	report.DeletedSessionID = append(report.DeletedSessionID, prune.DeletedSessionID...)
	report.BusySessionID = append(report.BusySessionID, prune.BusySessionID...)
}

// sessionStoreRootForCleanup resolves the canonical store root a cleanup
// directory targets. A path already naming a sessions-v4 root is used as-is;
// any other path goes through the legacy-dir mapping, so both the default CLI
// session dir and an explicitly passed sessions-v4 directory behave.
func sessionStoreRootForCleanup(dir string) string {
	if filepath.Base(dir) == "sessions-v4" {
		return dir
	}
	return session.RootForLegacyDir(dir)
}

// parseSessionDeleteAge parses a relative idle duration. Plain Go durations are
// accepted; a trailing "d" is expanded to 24h per day so the help text's 7d
// example works. An empty value yields a zero cutoff (no age filter).
func parseSessionDeleteAge(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := time.ParseDuration(days + "h")
		// 24*n is the day expansion; reject durations that would overflow it
		// instead of silently producing a cutoff in the far future.
		if err != nil || n < 0 || n > time.Duration(math.MaxInt64)/24 {
			return time.Time{}, fmt.Errorf("invalid --older-than %q", value)
		}
		return time.Now().Add(-24 * n), nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return time.Time{}, fmt.Errorf("invalid --older-than %q", value)
	}
	return time.Now().Add(-d), nil
}

func printSessionDeleteReport(report sessionDeleteReport, apply bool) {
	state := "deleted"
	if !apply {
		state = "would delete"
	}
	fmt.Printf("sessions matching: %d; %s: %d; busy: %d; unknown: %d; errors: %d\n",
		report.Matched, state, report.Deleted, report.Busy, report.Unknown, len(report.Errors))
	if !apply {
		fmt.Println("dry run; pass --apply to delete the matching sessions")
	}
	if len(report.DeletedSessionID) > 0 {
		limit := min(sessionDeletePreviewRows, len(report.DeletedSessionID))
		preview := report.DeletedSessionID[:limit]
		fmt.Printf("  %s: %s\n", state, strings.Join(preview, ", "))
		if len(report.DeletedSessionID) > limit {
			fmt.Printf("  ... and %d more\n", len(report.DeletedSessionID)-limit)
		}
	}
}
