// Package scrub removes personal data from text: serial numbers, MAC and IP addresses,
// hostnames, e-mail addresses and the user names in home-directory paths. DoesItOmarchy.com
// scrubs every submission with it before storing (PLAN §20.1); OmaBoot? scrubs bugreports
// with the same rules on the Mac and again on arrival (PF-13), so the rules live in one place.
package scrub

import (
	"regexp"
	"strings"
)

// Redacted replaces personal data.
const Redacted = "[redacted]"

// The rules: serial numbers, MAC addresses, IP addresses, hostnames, e-mail addresses
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
	// Peripheral batteries: the kernel names them hid-<uniq>-battery, and uniq is
	// often the device's serial ("hid-F0T036401FNJKNCAQ-battery-144").
	{regexp.MustCompile(`\bhid-[^\s"',;/]+-battery\b`), "hid-" + Redacted + "-battery"},
	// MAC addresses: 6 hex pairs separated by ":" or "-".
	{regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`), Redacted},
	// IPv6: hex groups with "::", or at least four colon-separated groups (times have two).
	{regexp.MustCompile(`(?i)(?:\b[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:)*[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){4,7}[0-9a-f]{1,4}\b`), Redacted},
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

// Text removes personal data from free text (notes, evidence, raw reports, logs).
func Text(s string) string {
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
		if systemdUnit.MatchString(m) {
			return m
		}
		return Redacted
	})
	for _, r := range scrubRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return scrubIPv4(s)
}

// IPv4 addresses. Version strings and USB port paths share the dotted shape:
// Apple's firmware "429.0.0.0.0" and the kernel's "usb 3-1.8.1.3" are kept,
// so a dotted run with more than four numbers, or one hanging off a USB bus
// number ("3-"), isn't an address.
var (
	ipv4Re     = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`)
	usbPathEnd = regexp.MustCompile(`(?:^|[^0-9A-Za-z.])\d{1,3}-$`)
)

func scrubIPv4(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range ipv4Re.FindAllStringIndex(s, -1) {
		before, after := s[:m[0]], s[m[1]:]
		if strings.HasSuffix(before, ".") || (len(after) > 1 && after[0] == '.' && after[1] >= '0' && after[1] <= '9') ||
			usbPathEnd.MatchString(before) {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(Redacted)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// personalKey matches structured fields whose whole value is personal.
var personalKey = regexp.MustCompile(`(?i)serial|uuid|hostname|host_name|mac_?addr|^mac$|ether|^ip$|ip_?addr|ipv[46]|user(name)?$|^login|email|owner|^address$|^peer$`)

// Value scrubs a decoded structure (the hardware probe): values under
// personal keys are replaced outright, and every other string is scrubbed.
func Value(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if personalKey.MatchString(strings.TrimSpace(k)) {
				out[k] = Redacted
				continue
			}
			out[k] = Value(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = Value(val)
		}
		return out
	case string:
		return Text(x)
	default:
		return v
	}
}
