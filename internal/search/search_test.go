package search

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/goccy/go-yaml"
)

type golden struct {
	Q        string              `yaml:"q"`
	Exactly  []string            `yaml:"exactly"`
	First    string              `yaml:"first"`
	Top      []string            `yaml:"top"`
	Order    []string            `yaml:"order"`
	Contains []string            `yaml:"contains"`
	Excludes []string            `yaml:"excludes"`
	Line     string              `yaml:"line"`
	Configs  map[string][]string `yaml:"configs"`
	Matched  map[string][]string `yaml:"matched"`
	None     bool                `yaml:"none"`
	Error    string              `yaml:"error"`
	Suggest  *bool               `yaml:"suggest"`
}

var testIndex *Index

func index(t testing.TB) *Index {
	t.Helper()
	if testIndex == nil {
		c, err := catalog.LoadFS(data.FS)
		if err != nil {
			t.Fatal(err)
		}
		testIndex = Build(c, nil)
	}
	return testIndex
}

func ids(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Identifier
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func configIDs(r Result) []string {
	var out []string
	for _, c := range r.Configs {
		out = append(out, c.ID)
	}
	return out
}

func TestGolden(t *testing.T) {
	ix := index(t)
	b, err := os.ReadFile("testdata/golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var cases []golden
	if err := yaml.UnmarshalWithOptions(b, &cases, yaml.Strict()); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 60 {
		t.Fatalf("golden set has %d queries, want at least 60", len(cases))
	}
	for _, g := range cases {
		t.Run(g.Q, func(t *testing.T) {
			resp := ix.Search(g.Q)
			got := ids(resp.Results)
			byID := map[string]Result{}
			for _, r := range resp.Results {
				byID[r.Identifier] = r
			}
			fail := func(format string, args ...any) {
				t.Helper()
				t.Errorf(format+"\n  got %d: %v", append(args, len(got), got)...)
			}
			if g.Exactly != nil && !sameSet(got, g.Exactly) {
				fail("want exactly %v", g.Exactly)
			}
			if g.First != "" && (len(got) == 0 || got[0] != g.First) {
				fail("want first %s", g.First)
			}
			if g.Top != nil && (len(got) < len(g.Top) || !sameSet(got[:len(g.Top)], g.Top)) {
				fail("want top %v", g.Top)
			}
			if g.Order != nil && strings.Join(got, " ") != strings.Join(g.Order, " ") {
				fail("want order %v", g.Order)
			}
			for _, id := range g.Contains {
				if _, ok := byID[id]; !ok {
					fail("missing %s", id)
				}
			}
			for _, id := range g.Excludes {
				if _, ok := byID[id]; ok {
					fail("must not contain %s", id)
				}
			}
			if g.Line != "" {
				if len(got) == 0 {
					fail("want results in line %s", g.Line)
				}
				for _, r := range resp.Results {
					if normID(r.Identifier) == "" || !strings.EqualFold(strings.ReplaceAll(r.LineName, " ", "-"), g.Line) {
						fail("%s is not in line %s", r.Identifier, g.Line)
					}
				}
			}
			for id, want := range g.Configs {
				r, ok := byID[id]
				if !ok || r.AllConfigs || !sameSet(configIDs(r), want) {
					fail("%s: want listed configs %v, got %v (all=%v)", id, want, configIDs(r), r.AllConfigs)
				}
			}
			for id, want := range g.Matched {
				if r := byID[id]; !sameSet(configIDs(r), want) {
					fail("%s: want matched configs %v, got %v", id, want, configIDs(r))
				}
			}
			if g.None && len(got) != 0 {
				fail("want no results")
			}
			if g.Error != "" && !strings.Contains(strings.Join(resp.Errors, "; "), g.Error) {
				t.Errorf("want error containing %q, got %v", g.Error, resp.Errors)
			}
			if g.Error == "" && len(resp.Errors) > 0 {
				t.Errorf("unexpected errors %v", resp.Errors)
			}
			if g.Suggest != nil && (resp.DidYouMean != "") != *g.Suggest {
				t.Errorf("did-you-mean %q, want present=%v", resp.DidYouMean, *g.Suggest)
			}
		})
	}
}

func TestLatency(t *testing.T) {
	ix := index(t)
	queries := []string{"mbp 2011", "gpu:nvidia year:<2011", "macbok pro", "retina macbook", "A1278", "cheese grater", "imca 2015", "zzzzzz"}
	var ds []time.Duration
	for i := 0; i < 50; i++ {
		for _, q := range queries {
			start := time.Now()
			ix.Search(q)
			ds = append(ds, time.Since(start))
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	p99 := ds[len(ds)*99/100]
	t.Logf("p50 %v, p99 %v over %d searches", ds[len(ds)/2], p99, len(ds))
	if limit := 5 * time.Millisecond; p99 > limit && !raceEnabled {
		t.Errorf("p99 %v exceeds %v", p99, limit)
	}
}

func BenchmarkSearch(b *testing.B) {
	ix := index(b)
	for i := 0; i < b.N; i++ {
		ix.Search("mbp 15 2012 gpu:nvidia")
	}
}

func BenchmarkBuild(b *testing.B) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		Build(c, nil)
	}
}

// Ports by name (PLAN §28.7): "thunderbolt 2" and "tb2" find exactly the
// configurations with a Thunderbolt 2 port, and so on for each version.
func TestPortNames(t *testing.T) {
	ix := index(t)
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	for port, queries := range map[string][]string{
		"thunderbolt-1":    {"thunderbolt 1", "tb1", "Thunderbolt1"},
		"thunderbolt-2":    {"thunderbolt 2", "tb2"},
		"thunderbolt-3":    {"thunderbolt 3", "tb3"},
		"firewire-800":     {"firewire 800", "fw800"},
		"mini-displayport": {"mini displayport", "mdp"},
	} {
		want := map[string]bool{}
		for _, m := range c.Macs {
			for _, r := range m.Releases {
				for _, cfg := range r.Configs {
					if cfg.Ports[port] > 0 {
						want[cfg.ID] = true
					}
				}
			}
		}
		for _, q := range queries {
			resp := ix.Search(q)
			got := map[string]bool{}
			for _, res := range resp.Results {
				for _, id := range configIDs(res) {
					got[id] = true
				}
			}
			if len(want) == 0 || len(got) != len(want) || len(resp.Errors) > 0 {
				t.Errorf("%q: %d configurations (errors %v), want the %d with %s", q, len(got), resp.Errors, len(want), port)
				continue
			}
			for id := range want {
				if !got[id] {
					t.Errorf("%q: missing %s", q, id)
				}
			}
		}
	}
	if n := len(index(t).Search("thunderbolt 2").Results); n == 0 {
		t.Error("thunderbolt 2 finds nothing")
	}
}
