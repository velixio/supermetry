package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"
	"sync"
)

// SaltLen is the salt size in bytes.
const SaltLen = 32

// SaltKey identifies the salt of one site-local day.
type SaltKey struct {
	Site int32
	Day  Day
}

// Salts holds random per-(site, local date) salts in memory (D5). The hot
// path only reads the map; a missing salt is created with crypto/rand and
// handed to the persist hook (the server persists it to PG; bulk load keeps
// it in memory only).
type Salts struct {
	mu      sync.RWMutex
	m       map[SaltKey][]byte
	persist func(SaltKey, []byte)
	random  io.Reader
}

// NewSalts returns an empty salt store. persist may be nil.
func NewSalts(persist func(SaltKey, []byte)) *Salts {
	return NewSaltsWithReader(persist, rand.Reader)
}

// NewSaltsWithReader returns an empty salt store using random for entropy.
// It exists so failure and deterministic generation can be tested; production
// callers use NewSalts, whose source is crypto/rand.Reader.
func NewSaltsWithReader(persist func(SaltKey, []byte), random io.Reader) *Salts {
	return &Salts{m: make(map[SaltKey][]byte), persist: persist, random: random}
}

// Get returns the salt for (site, day), creating a random one if needed.
func (s *Salts) Get(site int32, day Day) []byte {
	v, err := s.GetOrCreate(site, day)
	if err != nil {
		panic("generate visitor salt: " + err.Error())
	}
	return v
}

// GetOrCreate returns the salt for (site, day), creating a random one if
// needed. Entropy failures are returned and never install or persist a salt.
func (s *Salts) GetOrCreate(site int32, day Day) ([]byte, error) {
	k := SaltKey{site, day}
	s.mu.RLock()
	v, ok := s.m[k]
	s.mu.RUnlock()
	if ok {
		return v, nil
	}
	s.mu.Lock()
	v, ok = s.m[k]
	if !ok {
		v = make([]byte, SaltLen)
		if _, err := io.ReadFull(s.random, v); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		s.m[k] = v
	}
	s.mu.Unlock()
	if !ok && s.persist != nil {
		s.persist(k, v)
	}
	return v, nil
}

// Put installs a known salt (loaded from PG at startup).
func (s *Salts) Put(k SaltKey, salt []byte) {
	s.mu.Lock()
	s.m[k] = salt
	s.mu.Unlock()
}

// Purge deletes salts for days before keepFrom and returns how many went.
func (s *Salts) Purge(keepFrom Day) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k := range s.m {
		if k.Day < keepFrom {
			delete(s.m, k)
			n++
		}
	}
	return n
}

// Len returns the number of salts held.
func (s *Salts) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

var macPool = sync.Pool{New: func() any { return &macState{} }}

type macState struct {
	key []byte
	h   hash.Hash
	buf [sha256.Size]byte
}

// VisitorID = first 8 bytes of HMAC-SHA256(salt, site_id ‖ ip ‖ 0x00 ‖ ua) (D5).
// 64 bits keep collisions negligible (~N²/2⁶⁵) for the visitor counts of one
// site, and a stable bigint key for PG.
func VisitorID(salt []byte, site int32, ip, ua string) int64 {
	st := macPool.Get().(*macState)
	if st.h == nil || !hmac.Equal(st.key, salt) {
		st.key = append(st.key[:0], salt...)
		st.h = hmac.New(sha256.New, st.key)
	} else {
		st.h.Reset()
	}
	var sb [4]byte
	binary.BigEndian.PutUint32(sb[:], uint32(site))
	st.h.Write(sb[:])
	st.h.Write([]byte(ip))
	st.h.Write([]byte{0})
	st.h.Write([]byte(ua))
	sum := st.h.Sum(st.buf[:0])
	id := int64(binary.BigEndian.Uint64(sum[:8]))
	macPool.Put(st)
	return id
}
