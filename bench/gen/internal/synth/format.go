package synth

import (
	"strconv"
	"time"
)

// NDJSON formatting with append-only byte building. Field order is the
// CONTRACT's: ts, site, host, path, ref, utm_source, utm_medium, utm_campaign,
// name, props, ip, ua, screen_w, country, region, city, browser, os, device,
// revenue, vk, sk.

const internalTail = `","utm_source":"","utm_medium":"","utm_campaign":"","name":"`

var octet [256]string

func init() {
	for i := range octet {
		octet[i] = strconv.Itoa(i)
	}
}

const hexdigits = "0123456789abcdef"

func appendHex16(b []byte, x uint64) []byte {
	var h [16]byte
	for i := 15; i >= 0; i-- {
		h[i] = hexdigits[x&15]
		x >>= 4
	}
	return append(b, h[:]...)
}

func appendIP(b []byte, ip uint32) []byte {
	b = append(b, octet[ip>>24]...)
	b = append(b, '.')
	b = append(b, octet[ip>>16&255]...)
	b = append(b, '.')
	b = append(b, octet[ip>>8&255]...)
	b = append(b, '.')
	return append(b, octet[ip&255]...)
}

func append2(b []byte, v int64) []byte { return append(b, byte('0'+v/10), byte('0'+v%10)) }

const dayMs = 86_400_000

type formatter struct {
	g      *Generator
	day    int64
	prefix []byte // "YYYY-MM-DDT"
}

// appendTS writes unix ms as 2006-01-02T15:04:05.000Z.
func (f *formatter) appendTS(b []byte, ms int64) []byte {
	day := ms / dayMs
	if ms < 0 && ms%dayMs != 0 {
		day--
	}
	if f.prefix == nil || day != f.day {
		f.day = day
		f.prefix = time.UnixMilli(day*dayMs).UTC().AppendFormat(f.prefix[:0], "2006-01-02T")
	}
	b = append(b, f.prefix...)
	rem := ms - day*dayMs
	b = append2(b, rem/3_600_000)
	b = append(b, ':')
	b = append2(b, rem/60_000%60)
	b = append(b, ':')
	b = append2(b, rem/1000%60)
	b = append(b, '.')
	r := rem % 1000
	return append(b, byte('0'+r/100), byte('0'+r/10%10), byte('0'+r%10), 'Z')
}

func (f *formatter) appendEvent(b []byte, u *unit, e *eventRec) []byte {
	g, s := f.g, u.site
	b = append(b, `{"ts":"`...)
	b = f.appendTS(b, u.start+int64(e.off))
	b = append(b, s.prefix...)
	b = append(b, s.paths[e.path]...)
	sess := &u.sessions[e.sess]
	if e.refPath == landing {
		b = append(b, g.channelChunk[sess.channel]...)
	} else {
		b = append(b, s.internalRef...)
		b = append(b, s.paths[e.refPath]...)
		b = append(b, internalTail...)
	}
	b = append(b, g.nameChunk[e.name]...)
	if e.name == evPageview {
		b = append(b, "{}"...)
	} else {
		b = append(b, propCombos[e.name][e.props].json...)
	}
	b = append(b, `,"ip":"`...)
	v := &u.visitors[sess.visitor]
	b = appendIP(b, v.ip)
	b = append(b, g.uaChunk[v.ua]...)
	b = strconv.AppendInt(b, int64(v.screen), 10)
	b = append(b, `,"country":"`...)
	country := countries[v.country].code
	b = append(b, country...)
	location := countryLocation(country, v.vk)
	b = append(b, `","region":"`...)
	b = append(b, location.region...)
	b = append(b, `","city":"`...)
	b = append(b, location.city...)
	b = append(b, g.uaTail[v.ua]...)
	b = append(b, `,"revenue":`...)
	if e.name == evSignup {
		plan := propCombos[e.name][e.props].plan
		amount := []string{"19.00", "49.00", "199.00"}[plan]
		b = append(b, `{"amount":"`...)
		b = append(b, amount...)
		b = append(b, `","currency":"USD"}`...)
	} else {
		b = append(b, "{}"...)
	}
	b = append(b, `,"vk":"`...)
	b = appendHex16(b, v.vk)
	b = append(b, `","sk":"`...)
	b = appendHex16(b, g.sk(s, u.day, sess.visitor, sess.ord))
	return append(b, "\"}\n"...)
}
