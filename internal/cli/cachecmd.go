package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/cache"
)

func newCacheCmd(settings rootSettings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Report what the response cache holds",
		Long: "cache prints how many answers --cache holds on disk, how many of them are for a model " +
			"alias, how much space they take and where they live.\n\n" +
			"The cache lives in ONESIE_CACHE_DIR, or else XDG_CACHE_HOME/onesie, or else the system's own " +
			"cache directory. cache clear empties it.",
		Example: "  onesie cache\n" +
			"  onesie cache clear",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}

			return fmt.Errorf("onesie: cache takes no argument, and its one subcommand is clear, not %s. "+
				"To ask it as a question, put it after --", args[0])
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cacheDir(settings)
			if err != nil {
				return err
			}

			if warnErr := warnReadable(cmd.ErrOrStderr(), settings, dir); warnErr != nil {
				return warnErr
			}

			summary, err := cache.Summarize(dir, nil)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), describeCache(summary, dir))

			return err
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Remove every answer from the response cache",
		Long: "clear removes every cached answer, and keeps the directory and its CACHEDIR.TAG. It refuses a " +
			"directory with no CACHEDIR.TAG, so it never empties a directory onesie did not make.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}

			return errors.New("onesie: cache clear takes no argument")
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := cacheDir(settings)
			if err != nil {
				return err
			}

			removed, err := cache.Clear(dir, nil)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "removed %s from %s\n", entries(removed), dir)

			return err
		},
	})

	return cmd
}

func cacheDir(settings rootSettings) (string, error) {
	return cache.Dir(configEnv(settings.lookupEnv, settings.homeDir, settings.goos))
}

func warnReadable(w io.Writer, settings rootSettings, dir string) error {
	// A directory that is missing or cannot be read is left to the summary that follows, and windows
	// carries no such modes.
	info, err := settings.stat(dir)
	if err == nil && settings.goos != "windows" && info.Mode().Perm()&0o077 != 0 {
		_, printErr := fmt.Fprintln(w, (&cache.ModeError{Dir: dir, Mode: info.Mode().Perm()}).Error())

		return printErr
	}

	return nil
}

func describeCache(summary cache.Summary, dir string) string {
	if summary.Entries == 0 {
		return "0 entries in " + dir
	}

	return fmt.Sprintf("%s, %d of them for an alias, %s in %s",
		entries(summary.Entries), summary.Aliases, size(summary.Bytes), dir)
}

func entries(count int) string {
	if count == 1 {
		return "1 entry"
	}

	return fmt.Sprintf("%d entries", count)
}

func size(bytes int64) string {
	switch {
	case bytes < 1<<10:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	}
}
