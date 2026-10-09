package session

import (
	"crypto/sha256"
	"sync"
)

type profileSessionKey struct {
	siteID    int32
	sessionID int64
}

type profileHashKey struct {
	siteID int32
	hash   [sha256.Size]byte
}

type profileLinks struct {
	mu         sync.RWMutex
	links      map[profileSessionKey]map[[sha256.Size]byte]struct{}
	blocked    map[profileSessionKey]map[[sha256.Size]byte]struct{}
	tombstones map[profileHashKey]struct{}
}

func (p *profileLinks) init() {
	p.links = make(map[profileSessionKey]map[[sha256.Size]byte]struct{})
	p.blocked = make(map[profileSessionKey]map[[sha256.Size]byte]struct{})
	p.tombstones = make(map[profileHashKey]struct{})
}

// LinkProfile records only a one-way hash of a live session's external
// identity. It returns false if the identity has already been tombstoned.
func (m *Memory) LinkProfile(siteID int32, sessionID int64, externalID string) bool {
	hash := sha256.Sum256([]byte(externalID))
	profile := profileHashKey{siteID: siteID, hash: hash}
	session := profileSessionKey{siteID: siteID, sessionID: sessionID}
	m.profiles.mu.Lock()
	defer m.profiles.mu.Unlock()
	if _, tombstoned := m.profiles.tombstones[profile]; tombstoned {
		addProfileHash(m.profiles.blocked, session, hash)
		removeProfileHash(m.profiles.links, session, hash)
		return false
	}
	addProfileHash(m.profiles.links, session, hash)
	return true
}

// TombstoneProfile unlinks every live session carrying the hashed identity
// and blocks replay uploads for those sessions until the tombstone expires.
func (m *Memory) TombstoneProfile(siteID int32, hash [sha256.Size]byte) {
	profile := profileHashKey{siteID: siteID, hash: hash}
	m.profiles.mu.Lock()
	m.profiles.tombstones[profile] = struct{}{}
	for session, linkedHashes := range m.profiles.links {
		if _, linked := linkedHashes[hash]; session.siteID == siteID && linked {
			removeProfileHash(m.profiles.links, session, hash)
			addProfileHash(m.profiles.blocked, session, hash)
		}
	}
	m.profiles.mu.Unlock()
}

// ExpireProfileTombstone removes one process-local tombstone and its replay
// blocks. A later identify call may link the session again.
func (m *Memory) ExpireProfileTombstone(siteID int32, hash [sha256.Size]byte) {
	m.profiles.mu.Lock()
	delete(m.profiles.tombstones, profileHashKey{siteID: siteID, hash: hash})
	for session, blockedHashes := range m.profiles.blocked {
		if _, blocked := blockedHashes[hash]; session.siteID == siteID && blocked {
			removeProfileHash(m.profiles.blocked, session, hash)
		}
	}
	m.profiles.mu.Unlock()
}

// ProfileBlocked reports whether replay collection is blocked for a session
// whose linked profile was erased.
func (m *Memory) ProfileBlocked(siteID int32, sessionID int64) bool {
	m.profiles.mu.RLock()
	blocked := len(m.profiles.blocked[profileSessionKey{siteID: siteID, sessionID: sessionID}]) != 0
	m.profiles.mu.RUnlock()
	return blocked
}

func addProfileHash(index map[profileSessionKey]map[[sha256.Size]byte]struct{}, session profileSessionKey, hash [sha256.Size]byte) {
	hashes := index[session]
	if hashes == nil {
		hashes = make(map[[sha256.Size]byte]struct{})
		index[session] = hashes
	}
	hashes[hash] = struct{}{}
}

func removeProfileHash(index map[profileSessionKey]map[[sha256.Size]byte]struct{}, session profileSessionKey, hash [sha256.Size]byte) {
	hashes := index[session]
	delete(hashes, hash)
	if len(hashes) == 0 {
		delete(index, session)
	}
}

func (m *Memory) dropProfileSession(siteID int32, sessionID int64) {
	key := profileSessionKey{siteID: siteID, sessionID: sessionID}
	m.profiles.mu.Lock()
	delete(m.profiles.links, key)
	delete(m.profiles.blocked, key)
	m.profiles.mu.Unlock()
}
