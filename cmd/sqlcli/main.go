// Command sqlcli is a terminal SQL client for Oracle databases.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/history"
	"sqlcli/internal/ui"
	"sqlcli/internal/version"
)

func main() {
	defPath, _ := config.DefaultPath()
	cfgPath := flag.String("config", defPath, "path to the TOML config file")
	connName := flag.String("c", "", "connect to this saved connection at start")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("sqlcli", version.Version)
		return
	}

	if err := run(*cfgPath, *connName); err != nil {
		fmt.Fprintln(os.Stderr, "sqlcli:", err)
		os.Exit(1)
	}
}

func run(cfgPath, connName string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if connName != "" {
		found := false
		for _, c := range cfg.Connections {
			found = found || strings.EqualFold(c.Name, connName)
		}
		if !found {
			return fmt.Errorf("no connection named %q in %s", connName, cfg.Path())
		}
	}

	histPath := cfg.History.Path
	if histPath == "" {
		if histPath, err = config.DefaultHistoryPath(); err != nil {
			return err
		}
	}
	hist, err := history.Open(histPath)
	if err != nil {
		return fmt.Errorf("history %s: %w", histPath, err)
	}
	defer hist.Close()

	m := ui.New(cfg, hist, connName)
	_, err = tea.NewProgram(m).Run()
	m.CloseAll() // rolls back any open transactions
	return err
}
