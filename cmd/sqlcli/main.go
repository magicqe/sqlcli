// Command sqlcli is a terminal SQL client for Oracle databases.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	tea "charm.land/bubbletea/v2"

	"sqlcli/internal/config"
	"sqlcli/internal/history"
	"sqlcli/internal/ui"
	"sqlcli/internal/version"
)

// options are the command-line settings.
type options struct {
	cfgPath      string
	connName     string // -c: connect to this saved connection at start
	sessionName  string // -s: load this saved session at start
	listSessions bool   // -l: print the saved sessions and exit
}

func main() {
	defPath, _ := config.DefaultPath()
	var o options
	flag.StringVar(&o.cfgPath, "config", defPath, "path to the TOML config file")
	flag.StringVar(&o.connName, "c", "", "connect to this saved connection at start")
	flag.StringVar(&o.sessionName, "s", "", "load this saved session (all its SQL tabs) at start")
	flag.BoolVar(&o.listSessions, "l", false, "list the saved sessions and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("sqlcli", version.Version)
		return
	}
	if err := run(o, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sqlcli:", err)
		os.Exit(1)
	}
}

func run(o options, out io.Writer) error {
	if o.connName != "" && o.sessionName != "" {
		return errors.New("use either -c or -s: a session brings its own connections")
	}
	cfg, err := config.Load(o.cfgPath)
	if err != nil {
		return err
	}

	if o.connName != "" {
		found := false
		for _, c := range cfg.Connections {
			found = found || strings.EqualFold(c.Name, o.connName)
		}
		if !found {
			return fmt.Errorf("no connection named %q in %s", o.connName, cfg.Path())
		}
	}

	hist, err := openHistory(cfg)
	if err != nil {
		return err
	}
	defer hist.Close()

	if o.listSessions {
		return printSessions(out, hist)
	}
	if o.sessionName != "" {
		if ok, err := hist.SessionExists(o.sessionName); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("no session named %q (sqlcli -l lists the saved sessions)", o.sessionName)
		}
	}

	m := ui.New(cfg, hist, o.connName)
	if o.sessionName != "" {
		m.StartWithSession(o.sessionName)
	}
	_, err = tea.NewProgram(m).Run()
	m.CloseAll() // rolls back any open transactions
	return err
}

func openHistory(cfg *config.Config) (*history.Store, error) {
	path := cfg.History.Path
	if path == "" {
		var err error
		if path, err = config.DefaultHistoryPath(); err != nil {
			return nil, err
		}
	}
	h, err := history.Open(path)
	if err != nil {
		return nil, fmt.Errorf("history %s: %w", path, err)
	}
	return h, nil
}

// printSessions writes the saved sessions as a table, newest first.
func printSessions(w io.Writer, h *history.Store) error {
	list, err := h.Sessions()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		_, err := fmt.Fprintln(w, "No saved sessions. In sqlcli, Ctrl-S saves the open tabs as a session.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSAVED\tTABS\tCONNECTIONS")
	for _, s := range list {
		conns := s.Connections
		if conns == "" {
			conns = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", s.Name, s.SavedAt.Format("2006-01-02 15:04"), s.Tabs, conns)
	}
	return tw.Flush()
}
