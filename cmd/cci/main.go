package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xiaoqianghan/cc-insights/internal/config"
	"github.com/xiaoqianghan/cc-insights/internal/otel"
	"github.com/xiaoqianghan/cc-insights/internal/proxy"
	"github.com/xiaoqianghan/cc-insights/internal/stats"
	"github.com/xiaoqianghan/cc-insights/internal/storage"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "cci",
		Short: "Claude Code Insights - OTEL metrics proxy and analytics",
	}

	rootCmd.AddCommand(
		serveCmd(),
		stopCmd(),
		statsCmd(),
		statusCmd(),
		configCmd(),
		migrateCmd(),
		uninstallLegacyCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	var daemonize bool
	var daemonized bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the OTEL metrics proxy server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check daemonized first to prevent recursive respawn:
			// the daemon child inherits args, so we must handle --daemonized
			// before checking -d/--daemon.
			if daemonize && !daemonized {
				fmt.Println("Starting cci proxy in background...")
				return proxy.Daemonize()
			}

			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := storage.Open(cfg.Storage.DBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			srv := proxy.New(cfg, db)

			if daemonized {
				if err := proxy.WritePIDFile(); err != nil {
					return fmt.Errorf("write pid file: %w", err)
				}
				defer proxy.RemovePIDFile()
			}

			// Handle graceful shutdown
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.StartWithIdleTimeout()
			}()

			select {
			case sig := <-sigCh:
				fmt.Printf("\nReceived %s, shutting down...\n", sig)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return srv.Shutdown(ctx)
			case err := <-errCh:
				return err
			}
		},
	}

	cmd.Flags().BoolVarP(&daemonize, "daemon", "d", false, "Run as background daemon")
	cmd.Flags().BoolVar(&daemonized, "daemonized", false, "Internal flag: indicates this is the daemon child process")
	_ = cmd.Flags().MarkHidden("daemonized")

	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running proxy server",
		RunE: func(cmd *cobra.Command, args []string) error {
			running, pid := proxy.IsRunning()
			if !running {
				fmt.Println("cci proxy is not running.")
				return nil
			}

			process, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("find process %d: %w", pid, err)
			}

			if err := process.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("send SIGTERM to %d: %w", pid, err)
			}

			fmt.Printf("Sent SIGTERM to cci proxy (PID %d).\n", pid)
			return nil
		},
	}
}

func statsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats [today|week|month|year]",
		Short: "Show usage statistics",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			period := "today"
			if len(args) > 0 {
				period = args[0]
			}

			days, label, err := parsePeriod(period)
			if err != nil {
				return err
			}

			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := storage.Open(cfg.Storage.DBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			// Cleanup expired data
			if cfg.Storage.RetentionDays > 0 {
				deleted, _ := db.CleanupExpired(cfg.Storage.RetentionDays)
				if deleted > 0 {
					fmt.Printf("Cleaned up %d expired records.\n", deleted)
				}
			}

			// Check if proxy is running
			running, _ := proxy.IsRunning()
			if !running {
				fmt.Println("Note: cci proxy is not running. Showing historical data only.")
			}

			result, err := stats.Query(db.RawDB(), days, label, cfg.Pricing)
			if err != nil {
				return fmt.Errorf("query stats: %w", err)
			}

			stats.Render(result)
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show proxy status and health",
		RunE: func(cmd *cobra.Command, args []string) error {
			running, pid := proxy.IsRunning()
			if !running {
				if !quiet {
					fmt.Println("cci proxy is not running.")
				}
				os.Exit(1)
			}

			if quiet {
				return nil
			}

			fmt.Printf("cci proxy is running (PID %d).\n", pid)

			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := storage.Open(cfg.Storage.DBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()

			// Show upstream failure count
			failures, err := db.GetStat("upstream_failures")
			if err == nil && failures != "" {
				fmt.Printf("Upstream failures: %s\n", failures)
			}

			// Count total records
			var count int64
			err = db.RawDB().QueryRow("SELECT COUNT(*) FROM metrics").Scan(&count)
			if err == nil {
				fmt.Printf("Total records: %d\n", count)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "No output; exit 0 if running, 1 otherwise")
	return cmd
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(cfg)
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Import JSONL files from raw data directory into the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			rawDir := config.DataDir() + "/raw"
			entries, err := os.ReadDir(rawDir)
			if err != nil {
				return fmt.Errorf("read raw directory %s: %w", rawDir, err)
			}

			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			db, err := storage.Open(cfg.Storage.DBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer db.Close()
			_ = cfg // config available if needed for migration

			var fileCount, totalRecords int
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				if len(name) < 6 || name[len(name)-6:] != ".jsonl" {
					continue
				}

				filePath := rawDir + "/" + name
				count, err := migrateFile(db, filePath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: error migrating %s: %v\n", name, err)
					continue
				}
				fileCount++
				totalRecords += count
				fmt.Printf("  Migrated %s: %d records\n", name, count)
			}

			fmt.Printf("\nDone: %d files, %d total records imported.\n", fileCount, totalRecords)
			return nil
		},
	}
}

func migrateFile(db *storage.DB, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	count := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		records, err := otel.Parse(line)
		if err != nil || len(records) == 0 {
			continue
		}
		if err := db.InsertMetrics(records); err != nil {
			continue
		}
		count += len(records)
	}
	return count, scanner.Err()
}

func uninstallLegacyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-legacy",
		Short: "Remove old Nginx/Vector configurations (preserves data)",
		RunE: func(cmd *cobra.Command, args []string) error {
			files := []string{
				"/opt/homebrew/etc/nginx/servers/cc-insights.conf",
				"/opt/homebrew/etc/vector/vector.yaml",
				"/usr/local/bin/cci",
			}

			for _, f := range files {
				if _, err := os.Stat(f); err == nil {
					if err := os.Remove(f); err != nil {
						fmt.Fprintf(os.Stderr, "  Warning: could not remove %s: %v\n", f, err)
					} else {
						fmt.Printf("  Removed %s\n", f)
					}
				}
			}

			fmt.Println("\nLegacy configs removed.")
			fmt.Println("Data preserved at ~/.claude/cc-insights/raw/ (safe to delete manually after migration).")
			fmt.Println("\nYou can now stop the old services:")
			fmt.Println("  brew services stop nginx")
			fmt.Println("  brew services stop vector")
			return nil
		},
	}
}

// parsePeriod converts a period string to days and label.
func parsePeriod(period string) (int, string, error) {
	switch period {
	case "today":
		return 1, "today", nil
	case "week":
		return 7, "week", nil
	case "month":
		return 30, "month", nil
	case "year":
		return 365, "year", nil
	default:
		return 0, "", fmt.Errorf("unknown period %q: use today, week, month, or year", period)
	}
}
