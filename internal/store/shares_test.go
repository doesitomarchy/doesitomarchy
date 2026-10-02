package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestShares(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	mbp := Share{Product: "MacBookPro8,2", BoardID: "mac-94245a3940c91c80", CPU: " Intel(R)  Core(TM) i7-2720QM CPU @ 2.20GHz ",
		PCI: []string{"8086:0126", "1002:6760", "8086:0126"}, Modified: "no", Release: "15-early-2011"}
	if added, err := st.AddShare(ctx, mbp); err != nil || !added {
		t.Fatalf("first share: %v %v", added, err)
	}
	if added, err := st.AddShare(ctx, mbp); err != nil || added {
		t.Fatalf("the same share again today is not stored: %v %v", added, err)
	}
	other := mbp
	other.Modified = "yes"
	if added, _ := st.AddShare(ctx, other); !added {
		t.Error("different answers make a different share")
	}
	if added, _ := st.AddShare(ctx, Share{Product: "MacBookPro18,1"}); !added {
		t.Error("identifiers the catalog lacks are welcome")
	}
	if added, _ := st.AddShare(ctx, Share{BoardID: "Mac-F2208EC8"}); !added {
		t.Error("old 8-digit board IDs")
	}
	if n, _ := st.SharesOn(ctx, Today()); n != 4 {
		t.Errorf("today: %d shares, want 4", n)
	}

	for name, bad := range map[string]Share{
		"empty":        {},
		"identifier":   {Product: "rm -rf /"},
		"board":        {BoardID: "Mac-XYZ"},
		"pci":          {Product: "MacBookPro8,2", PCI: []string{"0x8086:0x0126"}},
		"cpu":          {Product: "MacBookPro8,2", CPU: "Intel\x07hello"},
		"modified":     {Product: "MacBookPro8,2", Modified: "maybe"},
		"release":      {Product: "MacBookPro8,2", Release: "Early 2011!"},
		"release only": {BoardID: "Mac-F2208EC8", Release: "15-early-2011"},
		"too many": {Product: "MacBookPro8,2", PCI: func() (ids []string) {
			for i := 0; i <= MaxSharePCI; i++ {
				ids = append(ids, fmt.Sprintf("8086:%04x", i))
			}
			return
		}()},
	} {
		if _, err := st.AddShare(ctx, bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	groups, err := st.ShareGroups(ctx, false)
	if err != nil || len(groups) != 3 {
		t.Fatalf("groups: %+v %v", groups, err)
	}
	if g := groups[0]; g.Product != "" || g.Shares[0].BoardID != "Mac-F2208EC8" {
		t.Errorf("no-identifier group first: %+v", g)
	}
	var g ShareGroup
	for _, x := range groups {
		if x.Product == "MacBookPro8,2" {
			g = x
		}
	}
	if g.Unseen != 2 || len(g.Shares) != 2 {
		t.Fatalf("MacBookPro8,2: %+v", g)
	}
	sh := g.Shares[0]
	if sh.BoardID != "Mac-94245A3940C91C80" || sh.CPU != "Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz" ||
		strings.Join(sh.PCI, ",") != "1002:6760,8086:0126" || sh.SharedOn != Today() {
		t.Errorf("stored normalised: %+v", sh)
	}

	if n, err := st.ReviewShares(ctx, "MacBookPro8,2", "crh"); err != nil || n != 2 {
		t.Fatalf("review: %d %v", n, err)
	}
	if groups, _ := st.ShareGroups(ctx, false); len(groups) != 2 {
		t.Errorf("a reviewed group drops out of the default list: %d groups", len(groups))
	}
	all, _ := st.ShareGroups(ctx, true)
	for _, x := range all {
		if x.Product == "MacBookPro8,2" && (x.Unseen != 0 || x.Reviewed == "" || x.Shares[0].ReviewedBy != "crh") {
			t.Errorf("reviewed group: %+v", x)
		}
	}
	// A new share brings the group back.
	third := mbp
	third.Modified = "unsure"
	st.AddShare(ctx, third)
	groups, _ = st.ShareGroups(ctx, false)
	back := false
	for _, x := range groups {
		back = back || (x.Product == "MacBookPro8,2" && x.Unseen == 1 && len(x.Shares) == 3)
	}
	if !back {
		t.Errorf("a new share returns the group: %+v", groups)
	}
}
