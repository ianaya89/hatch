package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// A fixed default port keeps the bootstrap command (and its QR) identical
// across runs; serve falls back to a random port if it's taken.
const defaultPort = 7788

type serveOptions struct {
	port        int
	code        string
	noMDNS      bool
	noBootstrap bool
	noQR        bool
	noCustom    bool
}

type inventoryReply struct {
	Items    []Item   `json:"items"`
	Warnings []string `json:"warnings,omitempty"`
	Hints    []string `json:"hints,omitempty"`
}

func hostName() string {
	h, _ := os.Hostname()
	return strings.TrimSuffix(h, ".local")
}

func userName() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func runServe(cfg Config, home string, opts serveOptions) error {
	host := hostName()
	defer keepAwake()()
	fmt.Println(styleTitle.Render(" hatch ") + styleDim.Render("  serve · "+host))
	fmt.Println()

	inv := scanInventory(cfg, home, func(stage string) {
		statusf("  %s scanning %s…", styleAccent.Render("●"), stage)
	})
	clearStatus()
	printSummary(os.Stdout, inv.items())
	fmt.Println()

	code := opts.code
	if code == "" {
		code = newCode()
	}
	np, code, err := parseCode(code)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(opts.port))
	if err != nil && opts.port == defaultPort {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if !opts.noMDNS {
		srv, err := advertise(np, port, host)
		if err != nil {
			fmt.Println(styleWarn.Render("  mDNS unavailable: " + err.Error() + " (use --addr on the other side)"))
		} else {
			defer srv.Shutdown()
		}
	}

	var boot *bootstrap
	if !opts.noBootstrap {
		if boot, err = loadBootstrap(port); err != nil {
			fmt.Println(styleWarn.Render("  binary bootstrap unavailable: " + err.Error()))
		}
	}

	fmt.Println("  code  " + styleCode.Render(" "+code+" "))
	fmt.Println()
	fmt.Println(styleDim.Render("  on the new machine:  ") + "hatch pull " + code)
	if boot != nil {
		cmd := boot.command(code)
		fmt.Println()
		if !opts.noQR && isTTY(os.Stdout) {
			printQR(cmd, []string{
				styleBold.Render("no hatch on the new Mac yet?"),
				"1. scan this with your iPhone camera",
				"2. tap the text → Copy",
				"3. ⌘V in Terminal on the new Mac",
				styleDim.Render("   (downloads, verifies sha256, pairs)"),
				styleDim.Render("   " + boot.platform + " · same Apple ID for Universal Clipboard"),
			})
		} else {
			fmt.Println(styleDim.Render("  no hatch there yet? run on the new machine (" + boot.platform + "):"))
		}
		fmt.Println("  " + styleDim.Render(cmd))
	}
	fmt.Println()
	if runtime.GOOS == "darwin" {
		fmt.Println(styleDim.Render("  this Mac won't idle-sleep while hatch runs (caffeinate)"))
	}
	if ips := localIPv4s(); len(ips) > 0 {
		var addrs []string
		for _, ip := range ips {
			addrs = append(addrs, net.JoinHostPort(ip, strconv.Itoa(port)))
		}
		fmt.Println(styleDim.Render("  no mDNS? add --addr " + strings.Join(addrs, " | ")))
	}
	fmt.Println()

	logf := func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		br := bufio.NewReaderSize(conn, 256<<10)
		conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		if isHTTPRequest(br) {
			if boot != nil {
				go boot.serve(conn, br, logf)
			} else {
				conn.Close()
			}
			continue
		}
		sc, err := serverHandshake(conn, br, code, np)
		if errors.Is(err, errBadCode) {
			conn.Close()
			return fmt.Errorf("wrong code attempt from %s — code burned, run hatch serve again", conn.RemoteAddr())
		}
		if err != nil {
			logf("%s ignored %s: %v", styleDim.Render("·"), conn.RemoteAddr(), err)
			conn.Close()
			continue
		}
		err = serveSession(sc, inv, host, !opts.noCustom, logf)
		sc.Close()
		return err
	}
}

func serveSession(sc *secureConn, inv *inventory, host string, allowCustom bool, logf func(string, ...any)) error {
	var peer hello
	if err := sc.recvJSON(&peer); err != nil {
		return err
	}
	if peer.Version != protoVersion {
		err := fmt.Errorf("protocol mismatch: peer v%d, us v%d", peer.Version, protoVersion)
		sc.sendError(err)
		return err
	}
	if err := sc.sendJSON(hello{Version: protoVersion, Host: host, User: userName(), OS: runtime.GOOS}); err != nil {
		return err
	}
	logf("%s paired with %s — the new machine should show this same cloud, spinning in sync:", styleOK.Render("✓"), peer.Host)
	logf("")
	stopVisual := presentVisual(newVisual(sc.visual), logf)
	defer stopVisual()

	var total int64
	var sent int
	for {
		var req request
		if err := sc.recvJSON(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("peer disconnected")
			}
			return err
		}
		switch req.Op {
		case "inventory":
			if err := sc.sendJSON(inventoryReply{Items: inv.items(), Warnings: inv.warnings, Hints: inv.hints}); err != nil {
				return err
			}
		case "fetch":
			src := inv.byID[req.ID]
			if src == nil {
				sc.sendError(fmt.Errorf("unknown item %q", req.ID))
				continue
			}
			stopVisual()
			n, err := sendSource(sc, src, req)
			if err != nil {
				return err
			}
			total += n
			sent++
			logf("%s %s %s", styleAccent.Render("→"), src.Title(), styleDim.Render(humanBytes(n)))
		case "children":
			src := inv.byID[req.ID]
			if src == nil {
				sc.sendError(fmt.Errorf("unknown item %q", req.ID))
				continue
			}
			if err := sc.sendJSON(src.children(compilePatterns(req.Exclude))); err != nil {
				return err
			}
		case "add":
			if !allowCustom {
				sc.sendError(errors.New("custom paths are disabled on this machine (serve --no-custom)"))
				continue
			}
			var rep addReply
			for _, p := range req.Paths {
				src, err := inv.addCustom(p)
				if err != nil {
					rep.Errors = append(rep.Errors, err.Error())
					continue
				}
				rep.Items = append(rep.Items, src.Item)
				stopVisual()
				logf("%s %s · %s · %s", styleAccent.Render("+"), src.Title(), plural(src.Files, "file"), humanBytes(src.Size))
			}
			if err := sc.sendJSON(rep); err != nil {
				return err
			}
		case "bye":
			stopVisual()
			logf("%s done · %s · %s", styleOK.Render("✓"), plural(sent, "item"), humanBytes(total))
			return nil
		default:
			sc.sendError(fmt.Errorf("unknown op %q", req.Op))
		}
	}
}
