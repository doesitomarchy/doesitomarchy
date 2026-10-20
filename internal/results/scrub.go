package results

import "github.com/doesitomarchy/doesitomarchy/pkg/scrub"

// Personal data is removed before a submission is stored (PLAN §20.1), by pkg/scrub: the same
// rules OmaBoot? applies to bugreports (PF-13).

// Redacted replaces personal data in stored text.
const Redacted = scrub.Redacted

// Scrub removes personal data from free text (notes, evidence, raw reports).
func Scrub(s string) string { return scrub.Text(s) }

// ScrubValue scrubs a decoded structure (the hardware probe): values under personal keys are
// replaced outright, and every other string is scrubbed.
func ScrubValue(v any) any { return scrub.Value(v) }
