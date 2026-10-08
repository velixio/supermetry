package writer

// ProfileTombstones is the process-local identity-erasure lookup used at the
// final persistence boundary. It must not perform database reads.
type ProfileTombstones interface {
	Contains(siteID int32, externalID string) bool
}

func (w *Writer) SetProfileTombstones(tombstones ProfileTombstones) {
	w.profileTombstones = tombstones
}

func (w *Writer) suppressTombstonedProfiles(events []Event) {
	if w.profileTombstones == nil {
		return
	}
	for index := range events {
		event := &events[index]
		if event.ProfileExternalID != "" && w.profileTombstones.Contains(event.SiteID, event.ProfileExternalID) {
			event.ProfileExternalID = ""
			event.ProfileTraits = nil
			event.ProfileID = 0
		}
	}
}
