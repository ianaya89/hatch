package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

const usage = `hatch — move your dev setup to a new machine, peer to peer.

usage:
  hatch serve [--port N] [--no-mdns] [--no-qr] [--no-bootstrap] [--no-custom]
                                              on the old machine: scan and wait for a pull;
                                              also serves its own binary + a verified install command
  hatch pull [code] [flags]                   on the new machine: pick items and pull them
  hatch scan [--json]                         preview what serve would offer
  hatch config [--init]                       print the effective config (or write the default)
  hatch version

pull flags:
  --addr host:port    skip mDNS and dial directly (e.g. a Tailscale IP)
  --update            replace local files when the peer's copy is newer
  --overwrite         replace every local file that differs (default: keep them)
  --fresh             ignore the selection remembered from the last pull
  --compress mode     auto | on | off (auto: off on Thunderbolt/direct links)
  --jobs N            parallel git clones (default 4)
  --yes               no TUI: pull the default selection and print progress

env:
  HATCH_HOME          act on this directory instead of $HOME
  HATCH_CONFIG        config file (default ~/.config/hatch/config.toml)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	home := homeDir()
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(home, args)
	case "pull":
		err = cmdPull(home, args)
	case "scan":
		err = cmdScan(home, args)
	case "config":
		err = cmdConfig(home, args)
	case "version", "--version", "-v":
		fmt.Println("hatch", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, styleErr.Render("error: ")+err.Error())
		os.Exit(1)
	}
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseInterleaved lets flags appear after positional args (hatch pull CODE --yes).
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func cmdServe(home string, args []string) error {
	fs := newFlags("serve")
	var opts serveOptions
	fs.IntVar(&opts.port, "port", defaultPort, "")
	fs.BoolVar(&opts.noMDNS, "no-mdns", false, "")
	fs.StringVar(&opts.code, "code", "", "")
	fs.BoolVar(&opts.noBootstrap, "no-bootstrap", false, "")
	fs.BoolVar(&opts.noQR, "no-qr", false, "")
	fs.BoolVar(&opts.noCustom, "no-custom", false, "")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig(home)
	if err != nil {
		return err
	}
	return runServe(cfg, home, opts)
}

func cmdPull(home string, args []string) error {
	fs := newFlags("pull")
	opts := pullOptions{}
	var addr string
	var yes bool
	fs.StringVar(&addr, "addr", "", "")
	fs.BoolVar(&opts.overwrite, "overwrite", false, "")
	fs.BoolVar(&opts.update, "update", false, "")
	fs.BoolVar(&opts.fresh, "fresh", false, "")
	fs.StringVar(&opts.compress, "compress", "auto", "")
	fs.IntVar(&opts.jobs, "jobs", 4, "")
	fs.BoolVar(&yes, "yes", false, "")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	switch opts.compress {
	case "auto", "on", "off":
	default:
		return fmt.Errorf("--compress must be auto, on or off")
	}
	code := strings.Join(pos, "-")
	defer keepAwake()()
	if yes {
		if code == "" {
			return fmt.Errorf("--yes needs the code as an argument")
		}
		return pullPlain(code, addr, home, opts)
	}

	m := newModel(code, addr, home, opts)
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	fm := final.(model)
	if fm.st != nil {
		fmt.Println(strings.Join(summaryLines(fm.st, true), "\n"))
	}
	if fm.err != nil {
		return fm.err
	}
	return nil
}

func pullPlain(code, addr, home string, opts pullOptions) error {
	ctx := context.Background()
	fmt.Fprintln(os.Stderr, "  finding peer and pairing…")
	sess, err := connect(ctx, code, addr)
	if err != nil {
		return err
	}
	var sel *savedSelection
	if !opts.fresh {
		sel = loadSelection(home, sess.peer.Host)
	}
	filters, kids := restoreSelection(sess, sel)
	opts.excludes = map[string][]string{}
	for id, f := range filters {
		opts.excludes[id] = f.excludes()
	}
	var items []Item
	selected := map[string]bool{}
	for _, it := range sess.items {
		on := it.Default
		if sel != nil {
			on = sel.apply(it)
		}
		selected[it.ID] = on
		if !on {
			continue
		}
		if rep, ok := kids[it.ID]; ok {
			it.Size = filters[it.ID].size(rep)
		}
		items = append(items, it)
	}
	var custom []string
	if sel != nil {
		custom = sel.Custom
		fmt.Fprintf(os.Stderr, "  restored the selection from %s (--fresh to ignore)\n", sel.Saved.Format("2006-01-02 15:04"))
	}
	selectionFrom(sess.items, selected, custom, filters).save(home, sess.peer.Host)
	fmt.Fprintf(os.Stderr, "  paired with %s via %s (%s) · %s\n", sess.peer.Host, sess.route, sess.addr, plural(len(items), "item"))
	vis := newVisual(sess.sc.visual)
	fmt.Fprintf(os.Stderr, "  compare with the cloud on %s:\n\n%s\n      %s\n\n", sess.peer.Host, vis.render(time.Now().UnixMilli()), vis.caption())
	st := newRunState(items)
	done := make(chan struct{})
	go func() {
		pull(ctx, sess, items, home, opts, st)
		close(done)
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			clearStatus()
			fmt.Println(strings.Join(summaryLines(st, false), "\n"))
			if st.failed > 0 {
				return fmt.Errorf("%d items failed", st.failed)
			}
			return nil
		case <-t.C:
			st.mu.Lock()
			cur := ""
			if st.current != "" {
				cur = st.byID[st.current].Title()
			}
			statusf("  %s / %s · %s · %s", humanBytes(st.doneBytes), humanBytes(st.totalBytes), st.phase, cur)
			st.mu.Unlock()
		}
	}
}

func cmdScan(home string, args []string) error {
	fs := newFlags("scan")
	asJSON := fs.Bool("json", false, "")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig(home)
	if err != nil {
		return err
	}
	inv := scanInventory(cfg, home, func(stage string) { statusf("  scanning %s…", stage) })
	clearStatus()
	items := inv.items()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(inventoryReply{Items: items, Warnings: inv.warnings, Hints: inv.hints})
	}
	var kind Kind
	for _, it := range items {
		if it.Kind != kind {
			kind = it.Kind
			fmt.Println("\n" + styleBold.Render(kindLabel[kind]))
		}
		mark := styleAccent.Render("●")
		if !it.Default {
			mark = styleDim.Render("○")
		}
		detail := it.Detail
		if it.Warn != "" {
			detail = styleWarn.Render("⚠ "+it.Warn) + " · " + detail
		}
		fmt.Printf("  %s %-44s %10s  %s\n", mark, it.Title(), humanBytes(it.Size), styleDim.Render(detail))
	}
	fmt.Println()
	printSummary(os.Stdout, items)
	if len(inv.warnings) > 0 {
		fmt.Println(styleDim.Render(fmt.Sprintf("\n  %d paths could not be read (e.g. %s)", len(inv.warnings), inv.warnings[0])))
	}
	if len(inv.hints) > 0 {
		fmt.Println("\n" + styleBold.Render("manual steps on the new machine"))
		for _, h := range inv.hints {
			fmt.Println("  • " + h)
		}
	}
	return nil
}

func cmdConfig(home string, args []string) error {
	fs := newFlags("config")
	initCfg := fs.Bool("init", false, "")
	if _, err := parseInterleaved(fs, args); err != nil {
		return err
	}
	if *initCfg {
		path, err := writeDefaultConfig(home)
		if err != nil {
			return err
		}
		fmt.Println("wrote", path)
		return nil
	}
	cfg, err := loadConfig(home)
	if err != nil {
		return err
	}
	fmt.Println("# " + configPath(home))
	fmt.Print(encodeConfig(cfg))
	return nil
}
