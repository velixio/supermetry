package writer

import (
	"panel/internal/dict"
	"panel/internal/privacy"
)

// SetPathPolicy sets the immutable server policy and cache used to recheck
// page-event provenance. Production configures this before Run. Validation
// never loads dictionary values from the database.
func (w *Writer) SetPathPolicy(policy privacy.PathPolicy, dictionary *dict.Dict) {
	w.trackingMu.Lock()
	w.pathPolicy, w.pathDictionary = policy, dictionary
	w.trackingMu.Unlock()
}

func (w *Writer) validatePathProvenance(e *Event, identityAllowed bool) {
	w.trackingMu.RLock()
	policy, dictionary := w.pathPolicy, w.pathDictionary
	w.trackingMu.RUnlock()
	valid := false
	anonymousPage := e.Kind == 0 && e.NameID == 0 && e.ProfileID == 0 &&
		e.ProfileExternalID == "" && e.ProfileTraits == nil && e.Props == nil
	if anonymousPage && e.PathSanitizerVersion == privacy.PathSanitizerVersion && dictionary != nil {
		if path, ok := dictionary.Value(dict.Path, e.PathID); ok {
			sanitized := policy.SanitizePath(path)
			valid = path == sanitized.Path && e.PathPolicyID == sanitized.Policy && e.PathSanitizerVersion == sanitized.Version
		}
	}
	if valid {
		return
	}
	// A safe-looking old value is not evidence of server sanitization. Never
	// promote an unverified record. Zero is the missing path sentinel: it avoids
	// creating a dictionary row during flush or retaining an unsafe queued path.
	e.PathSanitizerVersion, e.PathPolicyID = 0, ""
	if !identityAllowed {
		e.PathID = 0
	}
}
