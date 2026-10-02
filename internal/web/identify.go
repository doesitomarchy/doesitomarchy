package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
)

// PF-1 "Identify my Mac" (PLAN.md §22.6). The paste is parsed in the browser
// and only the extracted IDs reach the server, as a shareable URL. Without
// JavaScript the paste is posted, parsed here, and discarded at once.

// Commands visitors copy; both print only what /identify needs. The clipboard
// variants also put the output on the clipboard: sh -c groups the commands in
// any shell (fish has no { } or for/do), and tee /dev/tty keeps the output on
// screen. Neither command may contain a single quote.
const (
	identifyLinux = `cat /sys/class/dmi/id/product_name /sys/class/dmi/id/board_name; grep -m1 "model name" /proc/cpuinfo; for d in /sys/bus/pci/devices/*; do echo "pci $(cat $d/vendor):$(cat $d/device)"; done`
	identifyMacOS = `sysctl -n hw.model machdep.cpu.brand_string; ioreg -p IODeviceTree -r -n / -d 1 | grep board-id; system_profiler SPDisplaysDataType`
)

func toClipboard(cmd, tool string) string { return "sh -c '" + cmd + "' | tee /dev/tty | " + tool }

type identifyCommand struct {
	OS, Label, Where, Plain, Clip, Tool string
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
	Exact      bool
	Best       *configView
	Candidates []identifyCandidate
	By         string
}

func (s *Server) identify(w http.ResponseWriter, r *http.Request) {
	d := identifyData{Commands: []identifyCommand{
		{"linux", "Linux / Omarchy", "Linux, including Omarchy, in a terminal", identifyLinux, toClipboard(identifyLinux, "wl-copy"), "wl-copy"},
		{"macos", "macOS", "macOS, in Terminal", identifyMacOS, toClipboard(identifyMacOS, "pbcopy"), "pbcopy"},
	}}
	q := r.URL.Query()
	d.NoneFound = q.Get("none") != ""
	split := func(v string) []string {
		var out []string
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	d.Probe = match.Probe{ProductName: q.Get("product"), BoardID: q.Get("board"), PCI: split(q.Get("pci")), USB: split(q.Get("usb")), CPU: q.Get("cpu")}
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
	}
	title := "Identify my Mac"
	if d.Mac != nil {
		title = "Identify my Mac · " + d.Mac.Identifier
	}
	s.render(w, r, http.StatusOK, "identify", page{Title: title, Nav: "identify", Canonical: BaseURL + "/identify",
		Description: "Paste one command's output to identify your exact Intel Mac and configuration. Nothing is stored.", Data: d})
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
	target := "/identify"
	if len(q) > 0 {
		target += "?" + q.Encode()
	} else {
		target += "?none=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
