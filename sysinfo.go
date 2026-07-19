package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type infoField struct {
	Label string
	Value string
}

// osRelease parses /etc/os-release into a map.
func osRelease() map[string]string {
	m := map[string]string{}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[k] = strings.Trim(v, `"`)
	}
	return m
}

// osName is the branded name used for the window title and the alias.
func osName() string {
	if n := osRelease()["NAME"]; n != "" {
		return n
	}
	return "falcos"
}

// buildTag is the image build tag shown next to the OS name in the heading
// (os-release IMAGE_VERSION, e.g. 20260718). Empty when the image doesn't
// set it.
func buildTag() string {
	return osRelease()["IMAGE_VERSION"]
}

func run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gib(b int64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

func usedTotal(used, total int64) string {
	if total <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%s / %s (%d%%)", gib(used), gib(total), used*100/total)
}

func diskUsage(path string) string {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "n/a"
	}
	total := int64(st.Blocks) * st.Bsize
	free := int64(st.Bavail) * st.Bsize
	return usedTotal(total-free, total)
}

// rootDiskPath resolves the meaningful root filesystem. On bootc, "/" is a
// tiny read-only composefs (always ~100% full); the real storage is the
// physical root at /sysroot. Fall back to "/" off bootc.
func rootDiskPath() string {
	var st unix.Statfs_t
	if unix.Statfs("/sysroot", &st) == nil && int64(st.Blocks)*st.Bsize > 1<<30 {
		return "/sysroot"
	}
	return "/"
}

// ── fastfetch integration ────────────────────────────────────

// fastfetchInfo uses fastfetch --format json to populate the info fields.
// Falls back to the legacy shell-based gatherInfo if fastfetch is missing
// or returns an error.
func fastfetchInfo() ([]infoField, error) {
	out, err := exec.Command("fastfetch", "--format", "json", "--structure",
		"os:kernel:uptime:de:packages:cpu:gpu:memory:swap:disk:localip",
	).Output()
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Type   string          `json:"type"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, err
	}

	osr := osRelease()

	var fields []infoField
	for _, e := range entries {
		switch e.Type {
		case "OS":
			var r struct {
				PrettyName string `json:"prettyName"`
				VersionID  string `json:"versionID"`
				ID         string `json:"id"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			osLine := osr["PRETTY_NAME"]
			if osLine == "" {
				osLine = r.PrettyName + " " + r.VersionID
			}
			fields = append(fields, infoField{"OS", osLine})

		case "Kernel":
			var r struct {
				Release      string `json:"release"`
				Architecture string `json:"architecture"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			fields = append(fields, infoField{"Kernel", r.Release})

		case "Uptime":
			var r struct {
				Uptime int64 `json:"uptime"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			d := time.Duration(r.Uptime) * time.Millisecond
			days := int(d.Hours()) / 24
			hours := int(d.Hours()) % 24
			mins := int(d.Minutes()) % 60
			var uptimeStr string
			switch {
			case days > 0:
				uptimeStr = fmt.Sprintf("%dd %dh %dm", days, hours, mins)
			case hours > 0:
				uptimeStr = fmt.Sprintf("%dh %dm", hours, mins)
			default:
				uptimeStr = fmt.Sprintf("%dm", mins)
			}
			fields = append(fields, infoField{"Uptime", uptimeStr})

		case "Packages":
			var r struct {
				All           int `json:"all"`
				Rpm           int `json:"rpm"`
				FlatpakSystem int `json:"flatpakSystem"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			s := fmt.Sprintf("%d (rpm)", r.Rpm)
			if r.FlatpakSystem > 0 {
				s += fmt.Sprintf(", %d (flatpak)", r.FlatpakSystem)
			}
			fields = append(fields, infoField{"Packages", s})

		case "DE":
			var r struct {
				Name       string `json:"name"`
				PrettyName string `json:"prettyName"`
				Version    string `json:"version"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			de := r.PrettyName
			if de == "" {
				de = r.Name
			}
			if de == "" {
				de = os.Getenv("XDG_CURRENT_DESKTOP")
			}
			if r.Version != "" {
				de += " " + r.Version
			}
			if de == "" {
				de = "n/a"
			}
			fields = append(fields, infoField{"DE", de})

		case "CPU":
			var r struct {
				Name  string `json:"name"`
				Cores int    `json:"cores"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			cpu := r.Name
			if cpu == "" {
				cpu = "n/a"
			}
			if r.Cores > 0 {
				cpu += fmt.Sprintf(" (%d)", r.Cores)
			}
			fields = append(fields, infoField{"CPU", cpu})

		case "GPU":
			var gpus []struct {
				Name   string `json:"name"`
				Driver string `json:"driver"`
			}
			if json.Unmarshal(e.Result, &gpus) != nil {
				continue
			}
			for i, g := range gpus {
				label := fmt.Sprintf("GPU %d", i+1)
				val := g.Name
				if g.Driver != "" {
					val += " [" + g.Driver + "]"
				}
				fields = append(fields, infoField{label, val})
			}

		case "Memory":
			var r struct {
				Total int64 `json:"total"`
				Used  int64 `json:"used"`
			}
			if json.Unmarshal(e.Result, &r) != nil {
				continue
			}
			fields = append(fields, infoField{"Memory", usedTotal(r.Used, r.Total)})

		case "Swap":
			var swaps []struct {
				Used  int64 `json:"used"`
				Total int64 `json:"total"`
			}
			if json.Unmarshal(e.Result, &swaps) != nil {
				continue
			}
			var totalUsed, totalTotal int64
			for _, s := range swaps {
				totalUsed += s.Used
				totalTotal += s.Total
			}
			fields = append(fields, infoField{"Swap", usedTotal(totalUsed, totalTotal)})

		case "Disk":
			var disks []struct {
				Mountpoint string `json:"mountpoint"`
				Bytes      struct {
					Used  int64 `json:"used"`
					Total int64 `json:"total"`
				} `json:"bytes"`
			}
			if json.Unmarshal(e.Result, &disks) != nil {
				continue
			}
			// Show only meaningful mountpoints. BTRFS subvolumes
			// (/var, /var/home, /sysroot/ostree/...) share the same
			// backing store; skip them.
			seen := map[string]bool{}
			for _, d := range disks {
				mp := d.Mountpoint
				if mp == "/" || mp == "/etc" || mp == "/sysroot" {
					key := fmt.Sprintf("%d-%d", d.Bytes.Used, d.Bytes.Total)
					if seen[key] {
						continue
					}
					seen[key] = true
					fields = append(fields, infoField{"Disk (" + mp + ")", usedTotal(d.Bytes.Used, d.Bytes.Total)})
				}
			}

		case "LocalIp":
			var ips []struct {
				Name         string `json:"name"`
				DefaultRoute *struct {
					IPv4 bool `json:"ipv4"`
				} `json:"defaultRoute"`
				IPv4 string `json:"ipv4"`
			}
			if json.Unmarshal(e.Result, &ips) != nil {
				continue
			}
			for _, ip := range ips {
				if ip.DefaultRoute != nil && ip.DefaultRoute.IPv4 && ip.IPv4 != "" {
					ipStr := strings.SplitN(ip.IPv4, "/", 2)[0]
					fields = append(fields, infoField{"Local IP", ipStr + " (" + ip.Name + ")"})
					break
				}
			}
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no fields from fastfetch")
	}
	return fields, nil
}

// publicIP fetches the external IP and ISP via ipinfo.io.
// Returns "IP (ISP)" or empty on failure.
func publicIP() string {
	req, err := http.NewRequest("GET", "https://ipinfo.io/json", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "falcos-cli/1.0")
	c := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
	}}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var data struct {
		IP   string `json:"ip"`
		Org  string `json:"org"`
		City string `json:"city"`
	}
	if json.Unmarshal(body, &data) != nil || data.IP == "" {
		return ""
	}
	s := data.IP
	if data.Org != "" {
		s += " (" + data.Org + ")"
	}
	return s
}

// ── legacy fallback ──────────────────────────────────────────

func meminfo() map[string]int64 {
	m := map[string]int64{}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			m[strings.TrimSuffix(f[0], ":")] = v * 1024
		}
	}
	return m
}

func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "n/a"
	}
	model := ""
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			count++
			if model == "" {
				_, v, _ := strings.Cut(line, ":")
				model = strings.TrimSpace(v)
			}
		}
	}
	if model == "" {
		return "n/a"
	}
	return fmt.Sprintf("%s (%d)", model, count)
}

func gpus() []string {
	var out []string
	devs, _ := filepath.Glob("/sys/bus/pci/devices/*")
	sort.Strings(devs)
	for _, dev := range devs {
		class, err := os.ReadFile(filepath.Join(dev, "class"))
		if err != nil || !strings.HasPrefix(string(class), "0x03") {
			continue
		}
		addr := filepath.Base(dev)
		name := ""
		if line := run("lspci", "-mm", "-s", addr); line != "" {
			parts := strings.SplitN(line, `"`, 9)
			if len(parts) >= 8 {
				name = parts[3] + " " + parts[5]
			}
		}
		if name == "" {
			name = addr
		}
		if drv, err := os.Readlink(filepath.Join(dev, "driver")); err == nil {
			name += " [" + filepath.Base(drv) + "]"
		}
		out = append(out, name)
	}
	return out
}

func packages() string {
	rpmCount := 0
	if out := run("rpm", "-qa", "--qf", "x\n"); out != "" {
		rpmCount = strings.Count(out, "\n") + 1
	}
	s := fmt.Sprintf("%d (rpm)", rpmCount)
	if out := run("flatpak", "list", "--app", "--columns=application"); out != "" {
		s += fmt.Sprintf(", %d (flatpak)", strings.Count(out, "\n")+1)
	}
	return s
}

func desktop() string {
	de := os.Getenv("XDG_CURRENT_DESKTOP")
	if de == "" {
		return "n/a"
	}
	if de == "KDE" {
		if v := run("plasmashell", "--version"); v != "" {
			return "KDE Plasma " + strings.TrimPrefix(v, "plasmashell ")
		}
		return "KDE Plasma"
	}
	return de
}

func uptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "n/a"
	}
	secs, _ := strconv.ParseFloat(strings.Fields(string(data))[0], 64)
	d := time.Duration(secs) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func localIP() string {
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return "n/a"
	}
	defer conn.Close()
	ip := conn.LocalAddr().(*net.UDPAddr).IP
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
				return fmt.Sprintf("%s (%s)", ip, iface.Name)
			}
		}
	}
	return ip.String()
}

// gatherInfo collects system information, preferring fastfetch for speed.
func gatherInfo() []infoField {
	// fastfetch is near-instant; try it first.
	if fields, err := fastfetchInfo(); err == nil {
		return fields
	}

	// Legacy fallback.
	osr := osRelease()
	osLine := osr["PRETTY_NAME"]
	if osLine == "" {
		osLine = "n/a"
	}
	var un unix.Utsname
	kernel := "n/a"
	if err := unix.Uname(&un); err == nil {
		kernel = unix.ByteSliceToString(un.Release[:])
	}
	mi := meminfo()

	fields := []infoField{
		{"OS", osLine},
		{"Kernel", kernel},
		{"Uptime", uptime()},
		{"Packages", packages()},
		{"DE", desktop()},
		{"CPU", cpuModel()},
	}
	for i, g := range gpus() {
		fields = append(fields, infoField{fmt.Sprintf("GPU %d", i+1), g})
	}
	fields = append(fields,
		infoField{"Memory", usedTotal(mi["MemTotal"]-mi["MemAvailable"], mi["MemTotal"])},
		infoField{"Swap", usedTotal(mi["SwapTotal"]-mi["SwapFree"], mi["SwapTotal"])},
		infoField{"Disk (/)", diskUsage(rootDiskPath())},
		infoField{"Disk (/etc)", diskUsage("/etc")},
		infoField{"Local IP", localIP()},
	)
	return fields
}
