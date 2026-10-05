package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func validRequest() SourceRequest {
	return SourceRequest{SourceID: "Mac-Tester", Name: "Mac Tester", RepoURL: "https://github.com/example/mac-tester",
		Email: "dev@example.com", Description: "Checks Wi-Fi, sleep and audio on Intel Macs running Omarchy."}
}

func TestSourceRequestCheck(t *testing.T) {
	for _, c := range []struct {
		edit func(*SourceRequest)
		want string
	}{
		{func(r *SourceRequest) { r.SourceID = "1abc" }, "source ID"},
		{func(r *SourceRequest) { r.SourceID = "manual" }, "reserved"},
		{func(r *SourceRequest) { r.Name = " " }, "name"},
		{func(r *SourceRequest) { r.RepoURL = "http://github.com/x/y" }, "source code link"},
		{func(r *SourceRequest) { r.Homepage = "ftp://x.y" }, "homepage"},
		{func(r *SourceRequest) { r.Email = "Dev <dev@example.com>" }, "email"},
		{func(r *SourceRequest) { r.Description = strings.Repeat("x", 1001) }, "1,000"},
	} {
		r := validRequest()
		c.edit(&r)
		if err := r.Check(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	r := validRequest()
	if err := r.Check(); err != nil || r.SourceID != "mac-tester" {
		t.Errorf("valid request: %v (%s)", err, r.SourceID)
	}
}

// The whole life of a request: ask, approve, collect the key once, use it,
// lose it, get a new link.
func TestSourceRegistration(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	token, err := st.RequestSource(ctx, validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestSource(ctx, validRequest()); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("same ID while pending: %v", err)
	}
	if n, _ := st.PendingRequests(ctx); n != 1 {
		t.Errorf("pending: %d", n)
	}
	r, err := st.RequestByToken(ctx, token)
	if err != nil || r.State != RequestPending || r.SourceID != "mac-tester" {
		t.Fatalf("by token: %v %+v", err, r)
	}
	if _, err := st.KeyLinkByToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("key link before approval: %v", err)
	}
	if _, _, err := st.RedeemKeyLink(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("redeem before approval: %v", err)
	}
	edit := *r
	edit.SourceID = "mactester"
	if err := st.ApproveRequest(ctx, r.ID, edit, "crh"); err != nil {
		t.Fatal(err)
	}
	if err := st.ApproveRequest(ctx, r.ID, edit, "crh"); err == nil {
		t.Error("approved twice")
	}
	srcs, _ := st.Sources(ctx)
	var got *Source
	for i := range srcs {
		if srcs[i].ID == "mactester" {
			got = &srcs[i]
		}
	}
	if got == nil || got.HasKey || got.ContactEmail != "dev@example.com" || got.RepoURL == "" {
		t.Fatalf("source: %+v", got)
	}
	id, key, err := st.RedeemKeyLink(ctx, token)
	if err != nil || id != "mactester" || !strings.HasPrefix(key, KeyPrefix) {
		t.Fatalf("redeem: %v %s %s", err, id, key)
	}
	if _, _, err := st.RedeemKeyLink(ctx, token); !errors.Is(err, ErrLinkUsed) {
		t.Errorf("second redeem: %v", err)
	}
	if src, err := st.SourceByKey(ctx, key); err != nil || src.ID != "mactester" {
		t.Errorf("key works: %v", err)
	}
	// A new key link stops the old key at once.
	token2, err := st.NewKeyLink(ctx, "mactester", "crh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SourceByKey(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("old key after new link: %v", err)
	}
	token3, _ := st.NewKeyLink(ctx, "mactester", "crh")
	if _, _, err := st.RedeemKeyLink(ctx, token2); !errors.Is(err, ErrLinkUsed) {
		t.Errorf("superseded link: %v", err)
	}
	if _, key2, err := st.RedeemKeyLink(ctx, token3); err != nil || key2 == key {
		t.Errorf("new link: %v", err)
	}
	if _, err := st.NewKeyLink(ctx, "manual", "crh"); err == nil {
		t.Error("manual got a key link")
	}
}

func TestSourceRequestDeclineAndExpiry(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	token, _ := st.RequestSource(ctx, validRequest())
	r, _ := st.RequestByToken(ctx, token)
	if err := st.DeclineRequest(ctx, r.ID, " ", "crh"); err == nil {
		t.Error("declined without a reason")
	}
	if err := st.DeclineRequest(ctx, r.ID, "No source code at that link.", "crh"); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.RequestByToken(ctx, token); r.State != RequestDeclined || r.Reason == "" {
		t.Errorf("declined: %+v", r)
	}
	// The ID is free again once the request is decided.
	if _, err := st.RequestSource(ctx, validRequest()); err != nil {
		t.Errorf("reapply: %v", err)
	}
	// A declined request's email goes after 30 days.
	old := time.Now().UTC().Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	st.db.ExecContext(ctx, "UPDATE source_requests SET decided_at = ? WHERE id = ?", old, r.ID)
	st.SourceRequests(ctx, RequestDeclined)
	if r, _ := st.RequestByToken(ctx, token); r.Email != "" {
		t.Errorf("email kept: %q", r.Email)
	}
	// An unopened key link expires.
	pending, _ := st.SourceRequests(ctx, RequestPending)
	if err := st.ApproveRequest(ctx, pending[0].ID, pending[0], "crh"); err != nil {
		t.Fatal(err)
	}
	link, err := st.NewKeyLink(ctx, "mac-tester", "crh")
	if err != nil {
		t.Fatal(err)
	}
	st.db.ExecContext(ctx, "UPDATE key_links SET expires_at = ?", old)
	if _, _, err := st.RedeemKeyLink(ctx, link); !errors.Is(err, ErrLinkExpired) {
		t.Errorf("expired link: %v", err)
	}
}
