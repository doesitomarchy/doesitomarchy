package results

import _ "embed"

// SyntheticFixture is a made-up but realistic result for MacBookPro15,2
// (2018, four Thunderbolt 3 ports). Tests, demo mode and the release smoke
// test use it; it is never imported into production.
//
//go:embed fixtures/mbp152-synthetic.yaml
var SyntheticFixture []byte

// OmacDiagFixture is a made-up OmacDiag report for MacBookPro11,3 (PLAN §24):
// tests and demo mode import it.
//
//go:embed fixtures/omacdiag-mbp113.json
var OmacDiagFixture []byte
