// Package data embeds the catalog (CC BY-SA 4.0) into the doiomad binary, so a
// deploy ships code and data together. See catalog.LoadFS.
package data

import "embed"

// FS holds the catalog files, rooted at data/.
//
//go:embed vocabulary.yaml capabilities.yaml coverage.yaml aliases.yaml changelog.yaml config-ids.lock components/*.yaml macs/*.yaml sources/*.yaml layouts/*.yaml
var FS embed.FS
