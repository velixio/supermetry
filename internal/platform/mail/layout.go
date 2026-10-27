package mail

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
)

// LogoPath is where the dashboard serves the email logo for light backgrounds
// (web/public/email-logo.png).
const LogoPath = "/email-logo.png"

// LogoLightPath is the white logo shown by dark-mode clients
// (web/public/email-logo-light.png).
const LogoLightPath = "/email-logo-light.png"

// DocsURL is the public documentation linked from emails.
const DocsURL = "https://supermetry.com/docs"

// PrivacyURL is the public privacy page linked from the email footer.
const PrivacyURL = "https://supermetry.com/privacy"

// Row is one label and value line in a Section.
type Row struct {
	Label string
	Value string
}

// Link is a secondary link shown under the button.
type Link struct {
	Label string
	URL   string
}

// Step is one item of a numbered "get started" list.
type Step struct {
	Title string
	Body  string
}

type numberedStep struct {
	Num int
	Step
}

// Section is a tinted panel of label and value rows, such as the details of an
// invitation or the top pages of a report. Heading may be empty.
type Section struct {
	Heading string
	Rows    []Row
}

// layoutData is what the shared template renders.
type layoutData struct {
	Notice
	LogoURL      string
	LogoLightURL string
	HomeURL      string
	DocsURL      string
	PrivacyURL   string
	ActionOK     bool
	Action       string
	Links        []Link
	Steps        []numberedStep
}

// safeHTTPURL returns raw when it is an absolute http or https URL, else "".
func safeHTTPURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

// baseOf returns the cleaned public base URL, or "" when it is not usable.
func baseOf(raw string) string {
	return strings.TrimRight(safeHTTPURL(raw), "/")
}

// Palette comes from the logo: indigo, blue, teal, green. Every style is
// inline because many clients drop <style>; the one <style> block only adds
// dark-mode and small-screen refinements and is safe to lose. The gradient bar
// has a solid bgcolor fallback for Outlook.
var layoutHTML = template.Must(template.New("layout").Parse(`<!doctype html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="X-UA-Compatible" content="IE=edge">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>{{.Subject}}</title>
<style>
:root{color-scheme:light dark;supported-color-schemes:light dark}
@media (max-width:620px){.sm-w{width:100%!important}.sm-p{padding-left:22px!important;padding-right:22px!important}.sm-h1{font-size:24px!important}.sm-btn{display:block!important;text-align:center!important}}
@media (prefers-color-scheme:dark){
.dk-body{background-color:#0b0d12!important}
.dk-card{background-color:#151922!important;border-color:#262c3a!important}
.dk-h{color:#f9fafb!important}
.dk-text{color:#d1d5db!important}
.dk-muted{color:#9ca3af!important}
.dk-eyebrow{color:#a5b4fc!important}
.dk-panel{background-color:#1b2130!important;border-color:#2a3142!important}
.dk-rule{border-color:#2a3142!important}
.dk-link{color:#a5b4fc!important}
.dk-btn{background-color:#6366f1!important}
.dk-step{background-color:#6366f1!important}
.lg-light{display:none!important;max-height:0!important}
.lg-dark{display:block!important;max-height:none!important;overflow:visible!important}
}
</style>
</head>
<body class="dk-body" style="margin:0;padding:0;background-color:#f3f4f8;-webkit-text-size-adjust:100%;">
<div style="display:none;font-size:1px;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;mso-hide:all;">{{.Preheader}}{{if .Preheader}}&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;{{end}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f3f4f8" class="dk-body" style="background-color:#f3f4f8;">
<tr><td align="center" style="padding:28px 12px 24px 12px;">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" class="sm-w" style="width:600px;max-width:600px;">
<tr><td height="6" bgcolor="#4338ca" style="height:6px;line-height:6px;font-size:6px;background-color:#4338ca;background-image:linear-gradient(90deg,#6d3cf0 0%,#1f6fe8 40%,#0a93b8 75%,#0f9a6c 100%);border-radius:12px 12px 0 0;">&nbsp;</td></tr>
<tr><td class="sm-p dk-card" bgcolor="#ffffff" style="background-color:#ffffff;border:1px solid #e5e7eb;border-top:0;border-radius:0 0 12px 12px;padding:0 40px 40px 40px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#374151;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr><td align="left" style="padding:30px 0 26px 0;">
{{if .LogoURL}}<a href="{{.HomeURL}}" style="text-decoration:none;"><img class="lg-light" src="{{.LogoURL}}" width="170" height="35" alt="Supermetry" style="display:block;width:170px;height:35px;border:0;outline:none;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:22px;font-weight:700;color:#111827;"><img class="lg-dark" src="{{.LogoLightURL}}" width="170" height="35" alt="" style="display:none;max-height:0;overflow:hidden;mso-hide:all;width:170px;height:35px;border:0;"></a>{{else}}<div class="dk-h" style="font-size:24px;line-height:35px;font-weight:800;color:#111827;letter-spacing:-.01em;">Supermetry</div>{{end}}
</td></tr></table>
{{if .Eyebrow}}<div class="dk-eyebrow" style="margin:0 0 10px 0;font-size:12px;line-height:1.3;font-weight:700;letter-spacing:.09em;text-transform:uppercase;color:#4338ca;">{{.Eyebrow}}</div>{{end}}
{{if .Heading}}<h1 class="dk-h sm-h1" style="margin:0 0 16px 0;font-size:26px;line-height:1.25;font-weight:800;letter-spacing:-.01em;color:#111827;">{{.Heading}}</h1>{{end}}
{{range .Paragraphs}}<p class="dk-text" style="margin:0 0 16px 0;font-size:16px;line-height:1.6;color:#374151;">{{.}}</p>{{end}}
{{if .Items}}<ul class="dk-text" style="margin:0 0 16px 0;padding-left:22px;font-size:16px;line-height:1.6;color:#374151;">{{range .Items}}<li style="margin:0 0 4px 0;">{{.}}</li>{{end}}</ul>{{end}}
{{range .Sections}}{{if .Heading}}<div class="dk-muted" style="margin:8px 0 8px 0;font-size:12px;line-height:1.3;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:#6b7280;">{{.Heading}}</div>{{end}}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" class="dk-panel" bgcolor="#f5f6fb" style="margin:6px 0 22px 0;background-color:#f5f6fb;border:1px solid #e3e6f1;border-radius:10px;"><tr><td style="padding:4px 18px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
{{range $i, $r := .Rows}}<tr><td class="dk-muted dk-rule" valign="top" width="38%" style="padding:11px 12px 11px 0;{{if $i}}border-top:1px solid #e3e6f1;{{end}}font-size:14px;line-height:1.5;color:#6b7280;word-break:break-word;">{{$r.Label}}</td><td class="dk-h dk-rule" valign="top" align="right" style="padding:11px 0;{{if $i}}border-top:1px solid #e3e6f1;{{end}}font-size:14px;line-height:1.5;font-weight:600;color:#111827;text-align:right;word-break:break-word;">{{$r.Value}}</td></tr>{{end}}
</table></td></tr></table>{{end}}
{{if .Steps}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:10px 0 22px 0;">
{{range .Steps}}<tr><td valign="top" width="44" style="padding:0 0 18px 0;"><table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td class="dk-step" width="30" height="30" align="center" valign="middle" bgcolor="#4338ca" style="width:30px;height:30px;background-color:#4338ca;border-radius:15px;font-size:14px;line-height:30px;font-weight:700;color:#ffffff;text-align:center;">{{.Num}}</td></tr></table></td><td valign="top" style="padding:0 0 18px 0;"><div class="dk-h" style="font-size:16px;line-height:30px;font-weight:700;color:#111827;">{{.Title}}</div>{{if .Body}}<div class="dk-text" style="font-size:15px;line-height:1.55;color:#4b5563;">{{.Body}}</div>{{end}}</td></tr>{{end}}
</table>{{end}}
{{if .ActionOK}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" class="sm-w" style="margin:8px 0 18px 0;"><tr><td align="center" bgcolor="#4338ca" class="dk-btn" style="background-color:#4338ca;border-radius:8px;"><a class="sm-btn" href="{{.Action}}" target="_blank" style="display:inline-block;min-height:18px;padding:14px 30px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:16px;line-height:18px;font-weight:700;color:#ffffff;text-decoration:none;border-radius:8px;">{{.ActionLabel}}</a></td></tr></table>
<p class="dk-muted" style="margin:0 0 20px 0;font-size:12px;line-height:1.6;color:#6b7280;">Or paste this link into your browser:<br><a class="dk-link" href="{{.Action}}" style="color:#4f46e5;word-break:break-all;">{{.Action}}</a></p>{{end}}
{{range .Links}}<p class="dk-text" style="margin:0 0 10px 0;font-size:15px;line-height:1.5;"><a class="dk-link" href="{{.URL}}" style="color:#4338ca;font-weight:600;text-decoration:underline;">{{.Label}} &rarr;</a></p>{{end}}
{{if .Footer}}<p class="dk-muted dk-rule" style="margin:6px 0 0 0;padding-top:18px;border-top:1px solid #e5e7eb;font-size:13px;line-height:1.6;color:#6b7280;">{{.Footer}}</p>{{end}}
</td></tr>
<tr><td align="center" class="dk-muted" style="padding:22px 20px 0 20px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;line-height:1.7;color:#6b7280;">
<div style="margin:0 0 8px 0;">{{if .HomeURL}}<a class="dk-muted" href="{{.HomeURL}}/app" style="color:#4b5563;text-decoration:underline;">Dashboard</a> &nbsp;&middot;&nbsp; {{end}}<a class="dk-muted" href="{{.DocsURL}}" style="color:#4b5563;text-decoration:underline;">Docs</a> &nbsp;&middot;&nbsp; <a class="dk-muted" href="{{.PrivacyURL}}" style="color:#4b5563;text-decoration:underline;">Privacy</a></div>
{{if .Reason}}<div style="margin:0 0 6px 0;">{{.Reason}}</div>{{end}}
<div style="margin:0 0 6px 0;">Need help? Read the <a class="dk-muted" href="{{.DocsURL}}" style="color:#4b5563;text-decoration:underline;">documentation</a>.</div>
<div>Sent by Supermetry &middot; privacy-friendly web analytics</div>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`))

func renderLayout(n Notice) string {
	data := layoutData{Notice: n, HomeURL: baseOf(n.BaseURL), DocsURL: DocsURL, PrivacyURL: PrivacyURL}
	if data.HomeURL != "" {
		data.LogoURL = data.HomeURL + LogoPath
		data.LogoLightURL = data.HomeURL + LogoLightPath
	}
	for index, step := range n.Steps {
		data.Steps = append(data.Steps, numberedStep{Num: index + 1, Step: step})
	}
	if n.ActionLabel != "" {
		if action := safeHTTPURL(n.ActionURL); action != "" {
			data.ActionOK = true
			data.Action = action
		}
	}
	for _, link := range n.Links {
		if target := safeHTTPURL(link.URL); link.Label != "" && target != "" {
			data.Links = append(data.Links, Link{Label: link.Label, URL: target})
		}
	}
	var out bytes.Buffer
	if err := layoutHTML.Execute(&out, data); err != nil {
		panic(err)
	}
	return out.String()
}
