package main

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const (
	mdnsService  = "_hatch._tcp"
	mdnsDomain   = "local."
	lookupWindow = 8 * time.Second
)

func instanceName(nameplate int) string { return fmt.Sprintf("hatch-%d", nameplate) }

func advertise(nameplate, port int, host string) (*zeroconf.Server, error) {
	txt := []string{"v=" + strconv.Itoa(protoVersion), "host=" + host}
	return zeroconf.Register(instanceName(nameplate), mdnsService, mdnsDomain, port, txt, nil)
}

type peerAddr struct {
	addr  string
	route string
	host  string
}

func discover(ctx context.Context, nameplate int) ([]peerAddr, error) {
	ctx, cancel := context.WithTimeout(ctx, lookupWindow)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 16)
	go zeroconf.Lookup(ctx, instanceName(nameplate), mdnsService, mdnsDomain, entries)
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("no hatch serve with code %d found on the local network (try --addr host:port)", nameplate)
		case e, ok := <-entries:
			if !ok {
				return nil, fmt.Errorf("mDNS lookup ended without finding code %d", nameplate)
			}
			if e == nil || len(e.AddrIPv4) == 0 {
				continue
			}
			host := txtValue(e.Text, "host")
			var out []peerAddr
			for _, ip := range e.AddrIPv4 {
				out = append(out, peerAddr{addr: net.JoinHostPort(ip.String(), strconv.Itoa(e.Port)), route: describeRoute(ip), host: host})
			}
			sort.SliceStable(out, func(i, j int) bool { return routeRank(out[i].route) < routeRank(out[j].route) })
			return out, nil
		}
	}
}

func txtValue(txt []string, key string) string {
	for _, t := range txt {
		if v, ok := strings.CutPrefix(t, key+"="); ok {
			return v
		}
	}
	return ""
}

var tailscaleNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// macOS names the Thunderbolt Bridge bridge0; bridge100+ are VM/container
// networks (OrbStack, Docker, UTM) that the other machine can't reach.
func isVirtualIface(name string) bool {
	if strings.HasPrefix(name, "bridge") && name != "bridge0" {
		return true
	}
	for _, p := range []string{"vmnet", "docker", "veth", "awdl", "llw", "anpi", "ap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func describeRoute(ip net.IP) string {
	if ip.IsLoopback() {
		return "loopback"
	}
	if tailscaleNet.Contains(ip) {
		return "tailscale"
	}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || !n.Contains(ip) {
				continue
			}
			switch {
			case ifc.Name == "bridge0":
				return "thunderbolt"
			case isVirtualIface(ifc.Name):
				return "virtual"
			case ip.IsLinkLocalUnicast():
				return "direct link"
			default:
				return "lan"
			}
		}
	}
	return "routed"
}

func routeRank(r string) int {
	switch r {
	case "thunderbolt":
		return 0
	case "direct link":
		return 1
	case "loopback":
		return 2
	case "lan":
		return 3
	case "tailscale":
		return 4
	case "routed":
		return 5
	default:
		return 6
	}
}

func localIPv4s() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || isVirtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}
