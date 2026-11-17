package stripe

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	platformcrypto "supermetry/internal/platform/crypto"
)

// TokenTTL bounds how long an attribution token is accepted. A checkout that
// completes later is recorded without attribution.
const TokenTTL = 24 * time.Hour

// TokenPrefix marks Supermetry attribution tokens inside client_reference_id.
const TokenPrefix = "sm1_"

const (
	tokenVersion = 1
	tokenBytes   = 1 + 4 + 8 + 8 + 4*5 + 2 + 8
)

var ErrToken = errors.New("attribution token is invalid or expired")

// Attribution is the sealed content of a token: the live session that was
// active when the visitor started checkout and that session's acquisition
// dimensions as dictionary ids. It holds the same daily visitor hash that the
// session row already stores and nothing that identifies a person.
type Attribution struct {
	SiteID    int32
	SessionID int64
	VisitorID int64
	Source    int32
	UTMSource int32
	Medium    int32
	Campaign  int32
	Region    int32
	Country   string
	IssuedAt  time.Time
}

// Tokens seals and opens attribution tokens.
type Tokens struct {
	box *platformcrypto.SecretBox
}

// NewTokens returns a token codec. box must be dedicated to attribution tokens.
func NewTokens(box *platformcrypto.SecretBox) *Tokens { return &Tokens{box: box} }

// Seal returns "sm1_" followed by the base64url sealed attribution (about 104
// characters, below Stripe's 200 character client_reference_id limit).
func (t *Tokens) Seal(a Attribution) string {
	plain := make([]byte, tokenBytes)
	plain[0] = tokenVersion
	offset := 1
	put32 := func(v int32) { binary.BigEndian.PutUint32(plain[offset:], uint32(v)); offset += 4 }
	put64 := func(v int64) { binary.BigEndian.PutUint64(plain[offset:], uint64(v)); offset += 8 }
	put32(a.SiteID)
	put64(a.SessionID)
	put64(a.VisitorID)
	put32(a.Source)
	put32(a.UTMSource)
	put32(a.Medium)
	put32(a.Campaign)
	put32(a.Region)
	country := [2]byte{}
	if len(a.Country) == 2 {
		copy(country[:], a.Country)
	}
	copy(plain[offset:], country[:])
	offset += 2
	put64(a.IssuedAt.Unix())
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(t.box.Seal(plain))
}

// Open verifies a token for siteID and returns its attribution. It fails for
// tokens of another site, of another instance, tampered or older than TTL.
func (t *Tokens) Open(token string, siteID int32, now time.Time) (Attribution, error) {
	raw, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok || len(raw) > 256 {
		return Attribution{}, ErrToken
	}
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Attribution{}, ErrToken
	}
	plain, err := t.box.Open(sealed)
	if err != nil || len(plain) != tokenBytes || plain[0] != tokenVersion {
		return Attribution{}, ErrToken
	}
	offset := 1
	get32 := func() int32 { v := int32(binary.BigEndian.Uint32(plain[offset:])); offset += 4; return v }
	get64 := func() int64 { v := int64(binary.BigEndian.Uint64(plain[offset:])); offset += 8; return v }
	a := Attribution{SiteID: get32(), SessionID: get64(), VisitorID: get64()}
	a.Source, a.UTMSource, a.Medium, a.Campaign, a.Region = get32(), get32(), get32(), get32(), get32()
	if plain[offset] != 0 {
		a.Country = string(plain[offset : offset+2])
	}
	offset += 2
	a.IssuedAt = time.Unix(get64(), 0).UTC()
	if a.SiteID != siteID || now.Sub(a.IssuedAt) > TokenTTL || a.IssuedAt.Sub(now) > time.Minute {
		return Attribution{}, ErrToken
	}
	return a, nil
}
