package sentryingest

import (
	"supermetry/internal/appattach"
	"supermetry/internal/appingest"
)

// symbolicationAttachmentBytes caps the attachment content an event may carry
// through the symbolication queue, which is bounded by event count, not size.
const symbolicationAttachmentBytes = 128 << 10

// preparedAttachments is what Prepare decided for one envelope's attachments.
type preparedAttachments struct {
	appattach.Result
}

// prepareAttachments scrubs, classifies and caps the attachment items of an
// envelope. Nothing runs unless the envelope carries an event to attach them
// to. Oversize files are counted per item by the item loop; this counts what
// that loop cannot see: files dropped by the per-event cap and unsupported
// types or content.
func (h *Handler) prepareAttachments(items []Item) preparedAttachments {
	var in []appattach.Input
	hasEvent, oversize := false, 0
	for _, it := range items {
		switch it.Type {
		case "event":
			hasEvent = true
		case "attachment":
			in = append(in, appattach.Input{Name: it.Filename, ContentType: it.ContentType, AttachmentType: it.AttachmentType, Payload: it.Payload})
			if len(it.Payload) > MaxAttachment && it.AttachmentType != "event.minidump" {
				oversize++
			}
		}
	}
	if !hasEvent || len(in) == 0 {
		return preparedAttachments{}
	}
	res := appattach.Prepare(in, ScrubText)
	if n := res.TooLarge - oversize; n > 0 {
		h.stats.Add("attachment_too_large", uint64(n))
	}
	if res.Minidump > 0 {
		h.stats.Add("attachment_minidump_skipped", uint64(res.Minidump))
	}
	if res.Unsupported > 0 {
		h.stats.Add("attachment_unsupported", uint64(res.Unsupported))
	}
	return preparedAttachments{res}
}

// symbolicationAttachments returns the attachments to carry with an event that waits
// for symbolication: all of them when small, else metadata only.
func (h *Handler) symbolicationAttachments(p preparedAttachments) ([]appingest.AttachmentMeta, []appattach.File) {
	total := 0
	for _, f := range p.Files {
		total += len(f.Data)
	}
	if total <= symbolicationAttachmentBytes {
		return p.Metas, p.Files
	}
	h.stats.Add("attachment_symbolication_cap", uint64(len(p.Files)))
	metas := make([]appingest.AttachmentMeta, len(p.Metas))
	copy(metas, p.Metas)
	for i := range metas {
		metas[i].Stored = false
	}
	return metas, nil
}
