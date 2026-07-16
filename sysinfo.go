package main

import (
	"fmt"
	"net"
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

func run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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
			m[strings.TrimSuffix(f[0], ":")] = v * 1024 // kB -> bytes
		}
	}
	return m
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

// gpus lists PCI display-class devices as "name [driver]".
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
		// lspci -mm fields: slot "class" "vendor" "device" ...
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

// localIP finds the address used for the default route without sending
// packets, then names the owning interface.
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

func gatherInfo() []infoField {
	osr := osRelease()
	osLine := osr["PRETTY_NAME"]
	if osLine == "" {
		osLine = "n/a"
	}
	var un unix.Utsname
	kernel := "n/a"
	if err := unix.Uname(&un); err == nil {
		kernel = unix.ByteSliceToString(un.Release[:])
		osLine += " " + unix.ByteSliceToString(un.Machine[:])
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
