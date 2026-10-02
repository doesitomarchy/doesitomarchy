package web

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// accessVerifier checks the signed token Cloudflare Access adds to requests
// it let through (Cf-Access-Jwt-Assertion), so /admin can't be reached around
// Cloudflare even if someone found the origin (PLAN §22.5).
type accessVerifier struct {
	team     string // e.g. "doesitomarchy" (team domain doesitomarchy.cloudflareaccess.com)
	aud      string // the Access application's audience tag
	certsURL string
	client   *http.Client
	now      func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func newAccessVerifier(team, aud string) *accessVerifier {
	if team == "" || aud == "" {
		return nil
	}
	return &accessVerifier{team: team, aud: aud, certsURL: "https://" + team + ".cloudflareaccess.com/cdn-cgi/access/certs",
		client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
}

func (v *accessVerifier) issuer() string { return "https://" + v.team + ".cloudflareaccess.com" }

// key returns the signing key with this ID, refreshing the key set when the
// ID is unknown (Cloudflare rotates keys) but at most once a minute.
func (v *accessVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k := v.keys[kid]; k != nil && v.now().Sub(v.fetched) < 24*time.Hour {
		return k, nil
	}
	if v.now().Sub(v.fetched) < time.Minute && v.keys != nil {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Access keys: %w", err)
	}
	defer res.Body.Close()
	var set struct {
		Keys []struct {
			Kid, Kty, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("Access keys: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	v.keys, v.fetched = keys, v.now()
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

// verify checks a token and returns the e-mail address it was issued to.
func (v *accessVerifier) verify(ctx context.Context, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("not a JWT")
	}
	var header struct{ Alg, Kid string }
	if err := decodeSegment(parts[0], &header); err != nil {
		return "", err
	}
	if header.Alg != "RS256" {
		return "", fmt.Errorf("unexpected algorithm %q", header.Alg)
	}
	key, err := v.key(ctx, header.Kid)
	if err != nil {
		return "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return "", errors.New("bad signature")
	}
	var claims struct {
		Aud   audience `json:"aud"`
		Email string   `json:"email"`
		Exp   int64    `json:"exp"`
		Nbf   int64    `json:"nbf"`
		Iss   string   `json:"iss"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return "", err
	}
	now := v.now().Unix()
	switch {
	case !claims.Aud.has(v.aud):
		return "", errors.New("token is for another application")
	case claims.Iss != v.issuer():
		return "", fmt.Errorf("unexpected issuer %q", claims.Iss)
	case claims.Exp < now:
		return "", errors.New("token expired")
	case claims.Nbf > now+60:
		return "", errors.New("token not yet valid")
	case claims.Email == "":
		return "", errors.New("token has no e-mail address")
	}
	return claims.Email, nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// audience is a JWT "aud": a string or a list of strings.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	err := json.Unmarshal(b, &many)
	*a = many
	return err
}

func (a audience) has(s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
