package web

import (
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// PF-1 "Identify my Mac" (PLAN.md §22.6). The paste is parsed in the browser
// and only the extracted IDs reach the server, as a shareable URL. Without
// JavaScript the paste is posted, parsed here, and discarded at once.
// PF-2 explains the command and PF-3 lets visitors share the IDs (§23).

// Commands visitors copy; both print only what /identify needs. The clipboard
// variants also put the output on the clipboard: sh -c groups the commands in
// any shell (fish has no { } or for/do), and tee /dev/tty keeps the output on
// screen. Neither command may contain a single quote. Change a command and
// its explanation below together (TestIdentifyExplained checks).
const (
	identifyLinux = `cat /sys/class/dmi/id/product_name /sys/class/dmi/id/board_name; grep -m1 "model name" /proc/cpuinfo; for d in /sys/bus/pci/devices/*; do echo "pci $(cat $d/vendor):$(cat $d/device)"; done`
	identifyMacOS = `sysctl -n hw.model machdep.cpu.brand_string; ioreg -p IODeviceTree -r -n / -d 1 | grep board-id; system_profiler SPDisplaysDataType`
)

func toClipboard(cmd, tool string) string { return "sh -c '" + cmd + "' | tee /dev/tty | " + tool }

type identifyCommand struct {
	OS, Label, Where, Plain, Clip, Tool string
	Explain                             []explainRow
}

// explainRow is one part of a command, in the order it's written (PF-2).
type explainRow struct {
	Part     string        // the command's text
	What     template.HTML // written here, never from input
	Links    []docLink
	ClipOnly bool // only in the clipboard variant
}

type docLink struct{ Label, URL string }

// Official usage pages: man7.org for Linux, ss64.com for macOS (Apple no
// longer publishes current man pages), wl-clipboard's own project page.
var (
	manSh      = docLink{"sh(1p)", "https://man7.org/linux/man-pages/man1/sh.1p.html"}
	manTee     = docLink{"tee(1)", "https://man7.org/linux/man-pages/man1/tee.1.html"}
	clipSh     = explainRow{Part: "sh -c '…'", ClipOnly: true, Links: []docLink{manSh}, What: "Runs everything inside the quotes as one script, so the same line works in any shell: bash, zsh or fish."}
	semicolon  = explainRow{Part: ";", What: "Runs the next command once this one has finished."}
	explainTee = "Shows the output in your terminal as well, and passes it on. <code>/dev/tty</code> is your terminal."
)

var explainLinux = []explainRow{
	clipSh,
	{Part: "cat /sys/class/dmi/id/product_name /sys/class/dmi/id/board_name", Links: []docLink{{"cat(1)", "https://man7.org/linux/man-pages/man1/cat.1.html"}, {"sysfs(5)", "https://man7.org/linux/man-pages/man5/sysfs.5.html"}},
		What: "Prints two files in which your Mac's firmware reports its model identifier (e.g. <code>MacBookPro11,3</code>) and its board ID (<code>Mac-…</code>). <code>/sys</code> is where Linux shows hardware details as files."},
	semicolon,
	{Part: `grep -m1 "model name" /proc/cpuinfo`, Links: []docLink{{"grep(1)", "https://man7.org/linux/man-pages/man1/grep.1.html"}, {"proc_cpuinfo(5)", "https://man7.org/linux/man-pages/man5/proc_cpuinfo.5.html"}},
		What: "Prints the first line of <code>/proc/cpuinfo</code> that contains “model name”: your CPU's name. <code>-m1</code> stops after one match, as every core repeats it."},
	{Part: `for d in /sys/bus/pci/devices/*; do echo "pci $(cat $d/vendor):$(cat $d/device)"; done`, Links: []docLink{manSh, {"echo(1)", "https://man7.org/linux/man-pages/man1/echo.1.html"}},
		What: "Visits each PCI device the kernel found (graphics, Wi-Fi, controllers) and prints its vendor and device IDs, e.g. <code>pci 0x8086:0x0a26</code>. <code>$(…)</code> inserts what the command inside prints."},
	{Part: "| tee /dev/tty", ClipOnly: true, Links: []docLink{manTee}, What: template.HTML(explainTee)},
	{Part: "| wl-copy", ClipOnly: true, Links: []docLink{{"wl-clipboard", "https://github.com/bugaevc/wl-clipboard"}},
		What: "Puts the output on the clipboard, ready to paste below. It's part of wl-clipboard, which Omarchy includes."},
}

var explainMacOS = []explainRow{
	clipSh,
	{Part: "sysctl -n hw.model machdep.cpu.brand_string", Links: []docLink{{"sysctl", "https://ss64.com/mac/sysctl.html"}},
		What: "Reads two values macOS keeps about your Mac: its model identifier and its CPU's name. <code>-n</code> prints only the values."},
	semicolon,
	// ss64.com has no ioreg page; manpagez.com carries Apple's.
	{Part: "ioreg -p IODeviceTree -r -n / -d 1", Links: []docLink{{"ioreg(8)", "https://www.manpagez.com/man/8/ioreg/"}},
		What: "Shows the top of the device tree macOS received from the firmware. <code>-p IODeviceTree</code> picks that tree, <code>-n /</code> its root, <code>-r</code> only that part, and <code>-d 1</code> one level deep."},
	{Part: "| grep board-id", Links: []docLink{{"grep", "https://ss64.com/mac/grep.html"}},
		What: "Keeps only the line with your Mac's board ID."},
	{Part: "system_profiler SPDisplaysDataType", Links: []docLink{{"system_profiler", "https://ss64.com/mac/system_profiler.html"}},
		What: "The Graphics/Displays section of System Information: each GPU's name, vendor and device ID."},
	{Part: "| tee /dev/tty", ClipOnly: true, Links: []docLink{{"tee", "https://ss64.com/mac/tee.html"}}, What: template.HTML(explainTee)},
	{Part: "| pbcopy", ClipOnly: true, Links: []docLink{{"pbcopy", "https://ss64.com/mac/pbcopy.html"}},
		What: "Puts the output on the clipboard, ready to paste below."},
}

var identifyCommands = []identifyCommand{
	{"linux", "Linux / Omarchy", "Linux, including Omarchy, in a terminal", identifyLinux, toClipboard(identifyLinux, "wl-copy"), "wl-copy", explainLinux},
	{"macos", "macOS", "macOS, in Terminal", identifyMacOS, toClipboard(identifyMacOS, "pbcopy"), "pbcopy", explainMacOS},
}

type identifyCandidate struct {
	Config  *configView
	Matched int
}

type identifyData struct {
	Commands   []identifyCommand
	Asked      bool
	NoneFound  bool // a paste with nothing recognisable in it
	Probe      match.Probe
	Mac        *macView
	Title      string // the Mac's name, by the release identified when the candidates share one
	Exact      bool
	Best       *configView
	Candidates []identifyCandidate
	By         string
	// PF-3: sharing the IDs.
	Shared       bool   // just shared (or already shared today)
	ShareConsent bool   // the box wasn't ticked
	PCIList      string // the PCI IDs as the share form sends them
	Modified     []shareChoice
}

type shareChoice struct{ Value, Label string }

func (s *Server) identify(w http.ResponseWriter, r *http.Request) {
	d := identifyData{Commands: identifyCommands}
	q := r.URL.Query()
	d.NoneFound = q.Get("none") != ""
	d.Probe = match.Probe{ProductName: q.Get("product"), BoardID: q.Get("board"), PCI: splitList(q.Get("pci")), USB: splitList(q.Get("usb")), CPU: q.Get("cpu")}
	if d.Probe.ProductName != "" || d.Probe.BoardID != "" || len(d.Probe.PCI) > 0 {
		d.Asked = true
		res := s.match.Match(d.Probe)
		d.Exact, d.By = res.Exact, res.By
		view := s.data().view
		if res.Identifier != "" {
			d.Mac = view.bySlug[strings.ToLower(strings.ReplaceAll(res.Identifier, ",", "-"))]
		}
		for i, c := range res.Candidates {
			cv := view.configs[c.Config]
			// For a known Mac, only the best-scoring configurations: the
			// rest are ruled out by the devices pasted.
			if cv == nil || i >= 12 || (d.Mac != nil && c.Score < res.Candidates[0].Score) {
				continue
			}
			d.Candidates = append(d.Candidates, identifyCandidate{cv, len(c.Matched)})
		}
		if res.Exact && len(d.Candidates) > 0 {
			d.Best = d.Candidates[0].Config
		}
		if d.Mac != nil {
			cvs := []*configView{d.Best}
			if d.Best == nil {
				cvs = nil
				for _, c := range d.Candidates {
					cvs = append(cvs, c.Config)
				}
			}
			d.Title = d.Mac.Title
			if name := sharedName(cvs, func(cv *configView) string { return cv.ReleaseName }); name != "" {
				d.Title = name
			}
		}
		d.Shared = q.Get("shared") != ""
		d.ShareConsent = q.Get("share") == "consent"
		d.PCIList = strings.Join(d.Probe.PCI, ",")
		d.Modified = []shareChoice{{"yes", "Yes"}, {"no", "No"}, {"unsure", "Not sure"}}
	}
	title := "Identify my Mac"
	if d.Mac != nil {
		title = "Identify my Mac · " + d.Mac.Identifier
	}
	s.render(w, r, http.StatusOK, "identify", page{Title: title, Nav: "identify", Canonical: BaseURL + "/identify",
		Description: "Paste one command's output to identify your exact Intel Mac and configuration. Nothing is stored.", Data: d})
}

func splitList(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// identifyURL is the result page for a probe.
func identifyURL(p match.Probe) string {
	q := url.Values{}
	if p.ProductName != "" {
		q.Set("product", p.ProductName)
	}
	if p.BoardID != "" {
		q.Set("board", p.BoardID)
	}
	if p.CPU != "" {
		q.Set("cpu", p.CPU)
	}
	if len(p.PCI) > 0 {
		q.Set("pci", strings.Join(p.PCI, ","))
	}
	if len(q) == 0 {
		return "/identify?none=1"
	}
	return "/identify?" + q.Encode()
}

// identifyPost is the no-JavaScript path: extract the IDs, drop the paste,
// and redirect to the shareable result URL.
func (s *Server) identifyPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		io.Copy(io.Discard, r.Body)
		http.Redirect(w, r, "/identify", http.StatusSeeOther)
		return
	}
	p := match.ParseProbe(r.PostFormValue("paste"))
	r.PostForm, r.Form = nil, nil // the paste goes no further
	http.Redirect(w, r, identifyURL(p), http.StatusSeeOther)
}

// PF-3 limits (PLAN.md §23.1).
const (
	SharesPerHourPerIP = 10
	SharesPerDay       = 500
)

// identifyShare stores the IDs a visitor chose to share, then returns to
// their result. It stores no IP address, time of day or free text.
func (s *Server) identifyShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		io.Copy(io.Discard, r.Body)
		s.message(w, r, http.StatusRequestEntityTooLarge, "Too much to share", "That was more than a share can hold.", "/identify")
		return
	}
	if !sameOrigin(r) {
		s.message(w, r, http.StatusForbidden, "Couldn't share", "That form came from another site.", "/identify")
		return
	}
	sh := store.Share{Product: r.PostFormValue("product"), BoardID: r.PostFormValue("board"), CPU: r.PostFormValue("cpu"),
		PCI: splitList(r.PostFormValue("pci")), Modified: r.PostFormValue("modified"), Release: r.PostFormValue("release")}
	back := identifyURL(match.Probe{ProductName: sh.Product, BoardID: sh.BoardID, CPU: sh.CPU, PCI: sh.PCI})
	if r.PostFormValue("consent") != "yes" {
		http.Redirect(w, r, back+"&share=consent#share", http.StatusSeeOther)
		return
	}
	sh.Normalize()
	err := sh.Validate()
	if err == nil && sh.Release != "" && !s.hasRelease(sh.Product, sh.Release) {
		err = errBadRelease
	}
	if err != nil {
		s.message(w, r, http.StatusBadRequest, "Couldn't share", "Something in the IDs didn't look right, so nothing was shared. Identify your Mac again and retry.", "/identify")
		return
	}
	if !s.shareLimit.allow(clientIP(r), time.Now()) {
		w.Header().Set("Retry-After", "3600")
		s.message(w, r, http.StatusTooManyRequests, "Thanks, that's plenty", "We've had a lot of shares from your network in the last hour. Please try again later.", back)
		return
	}
	if n, err := s.store.SharesOn(r.Context(), store.Today()); err == nil && n >= SharesPerDay {
		w.Header().Set("Retry-After", "3600")
		s.message(w, r, http.StatusTooManyRequests, "Sharing is paused for today", "We've reached today's limit for shared IDs. Please try again tomorrow.", back)
		return
	}
	if _, err := s.store.AddShare(r.Context(), sh); err != nil {
		s.fail(w, r, err)
		return
	}
	// A repeat today gets the same thanks, so it reveals nothing.
	http.Redirect(w, r, back+"&shared=1#share", http.StatusSeeOther)
}

var errBadRelease = errors.New("release does not belong to the identifier")

func (s *Server) hasRelease(identifier, release string) bool {
	mv := s.data().view.bySlug[strings.ToLower(strings.ReplaceAll(identifier, ",", "-"))]
	if mv == nil || mv.Identifier != identifier {
		return false
	}
	for _, rv := range mv.Releases {
		if rv.ID == release {
			return true
		}
	}
	return false
}

// sameOrigin refuses a form posted from another site. Browsers send Origin
// on cross-site POSTs; a missing Origin (old browsers, curl) is allowed.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// clientIP is the visitor's address, for the share rate limit only; it's
// never logged or stored. The origin is reachable only through the
// Cloudflare tunnel, which sets Cf-Connecting-IP.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter counts events per key over a sliding window, in memory only.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	seen   map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, seen: map[string][]time.Time{}}
}

// allow records an event for key and reports whether it's within the limit.
func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	if len(l.seen) > 10000 { // forget idle keys now and then
		for k, ts := range l.seen {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(l.seen, k)
			}
		}
	}
	ts := l.seen[key]
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= l.limit {
		l.seen[key] = ts
		return false
	}
	l.seen[key] = append(ts, now)
	return true
}

// wait is how long until key may have another event: until its oldest one
// in the window leaves it.
func (l *rateLimiter) wait(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := l.seen[key]
	if len(ts) < l.limit {
		return 0
	}
	return max(0, ts[len(ts)-l.limit].Add(l.window).Sub(now))
}
