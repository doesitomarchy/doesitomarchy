package results

import (
	"regexp"
	"strings"
)

// Redacted replaces personal data in stored text.
const Redacted = "[redacted]"

// Personal data is removed before a submission is stored (PLAN §20.1):
// serial numbers, MAC addresses, IP addresses, hostnames, e-mail addresses
// and the user names in home-directory paths. The rules are deliberately
// greedy; an over-redacted log line is better than a leaked serial.
var scrubRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Values labelled as serials, UUIDs or hostnames: "serial: C02X…", "product_uuid=…", "Hostname: work-mbp".
	{regexp.MustCompile(`(?i)\b((?:[a-z_]*serial[a-z_ ]*?(?:number)?|[a-z_]*uuid|host ?name|static hostname|computer ?name)(?:\s*\([^)]*\))?\s*["']?\s*[:=]\s*["']?)([^\s"',;]+)`), "${1}" + Redacted},
	// "serial C02XG0FDH7JY" without a separator: only an upper-case code of
	// 8–20 characters, so "serial port" and the like survive.
	{regexp.MustCompile(`((?i:\bserial(?:\s+number)?)\s+)[A-Z0-9]{8,20}\b`), "${1}" + Redacted},
	// MAC addresses: 6 hex pairs separated by ":" or "-".
	{regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`), Redacted},
	// IPv6: hex groups with "::", or at least four colon-separated groups (times have two).
	{regexp.MustCompile(`(?i)(?:\b[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:)*[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){4,7}[0-9a-f]{1,4}\b`), Redacted},
	// IPv4.
	{regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`), Redacted},
	// User names in home-directory and removable-media paths.
	{regexp.MustCompile(`(/home/|/Users/|/var/home/|/run/media/|/media/)[^/\s"']+`), "${1}" + Redacted},
	// Shell prompts ("carl@work-mbp:~$", "[carl@work-mbp ~]$"). Only prompt-like
	// contexts, so systemd units such as getty@tty1.service survive.
	{regexp.MustCompile(`(?i)\b[a-z_][a-z0-9_-]*@[a-z0-9][a-z0-9-]*([:~ ]|\]|$)`), Redacted + "@" + Redacted + "${1}"},
}

// emailRe matches e-mail addresses. It runs before the other rules (the
// user@host rule would otherwise match half of one), and spares systemd unit
// names, which share the shape: getty@tty1.service, user@1000.service.
var (
	emailRe     = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)+\b`)
	systemdUnit = regexp.MustCompile(`(?i)\.(service|socket|target|mount|automount|timer|path|device|slice|scope|swap)$`)
)

// Scrub removes personal data from free text (notes, evidence, raw reports).
func Scrub(s string) string {
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
		if systemdUnit.MatchString(m) {
			return m
		}
		return Redacted
	})
	for _, r := range scrubRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// personalKey matches structured fields whose whole value is personal.
var personalKey = regexp.MustCompile(`(?i)serial|uuid|hostname|host_name|mac_?addr|^mac$|ether|^ip$|ip_?addr|ipv[46]|user(name)?$|^login|email|owner|^address$|^peer$`)

// ScrubValue scrubs a decoded structure (the hardware probe): values under
// personal keys are replaced outright, and every other string is scrubbed.
func ScrubValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if personalKey.MatchString(strings.TrimSpace(k)) {
				out[k] = Redacted
				continue
			}
			out[k] = ScrubValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = ScrubValue(val)
		}
		return out
	case string:
		return Scrub(x)
	default:
		return v
	}
}
