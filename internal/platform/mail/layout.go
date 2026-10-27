package mail

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
)

// LogoPath is where the dashboard serves the email logo (web/public/email-logo.png).
const LogoPath = "/email-logo.png"

// DocsURL is the public documentation linked from the welcome email.
const DocsURL = "https://supermetry.com/docs"

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

// Section is a small titled list of rows, such as the details of an invitation
// or the top pages of a report. Heading may be empty.
type Section struct {
	Heading string
	Rows    []Row
}

// layoutData is what the shared template renders.
type layoutData struct {
	Notice
	LogoURL  string
	HomeURL  string
	ActionOK bool
	Action   string
	Links    []Link
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

// Every style is inline: many clients drop <style>. The one <style> block only
// adds dark-mode and small-screen refinements and is safe to lose.
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
@media (max-width:620px){.sm-w{width:100%!important}.sm-p{padding:24px 20px!important}}
@media (prefers-color-scheme:dark){
.dk-body{background-color:#0f1115!important}
.dk-card{background-color:#181b21!important;border-color:#2a2f38!important}
.dk-text{color:#e8eaed!important}
.dk-muted{color:#9aa3af!important}
.dk-rule{border-color:#2a2f38!important}
}
</style>
</head>
<body class="dk-body" style="margin:0;padding:0;background-color:#f4f5f7;-webkit-text-size-adjust:100%;">
<div style="display:none;font-size:1px;line-height:1px;max-height:0;max-width:0;opacity:0;overflow:hidden;mso-hide:all;">{{.Preheader}}{{if .Preheader}}&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;&#8199;&#847;{{end}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f4f5f7" class="dk-body" style="background-color:#f4f5f7;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" class="sm-w" style="width:600px;max-width:600px;">
<tr><td align="left" bgcolor="#ffffff" style="background-color:#ffffff;border:1px solid #e4e7eb;border-bottom:0;border-radius:10px 10px 0 0;padding:20px 32px 4px 18px;">
{{if .LogoURL}}<a href="{{.HomeURL}}" style="text-decoration:none;"><img src="{{.LogoURL}}" width="240" height="56" alt="Supermetry" style="display:block;width:240px;max-width:100%;height:auto;border:0;outline:none;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:22px;font-weight:700;color:#17191c;"></a>{{else}}<div style="font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;font-size:24px;line-height:56px;font-weight:700;color:#17191c;">Supermetry</div>{{end}}
</td></tr>
<tr><td class="sm-p dk-card dk-text" bgcolor="#ffffff" style="background-color:#ffffff;border:1px solid #e4e7eb;border-top:0;border-radius:0 0 10px 10px;padding:16px 32px 32px 32px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#17191c;">
{{if .Heading}}<h1 class="dk-text" style="margin:8px 0 16px 0;font-size:22px;line-height:1.3;font-weight:700;color:#17191c;">{{.Heading}}</h1>{{end}}
{{range .Paragraphs}}<p class="dk-text" style="margin:0 0 16px 0;font-size:15px;line-height:1.6;color:#17191c;">{{.}}</p>{{end}}
{{if .Items}}<ul class="dk-text" style="margin:0 0 16px 0;padding-left:22px;font-size:15px;line-height:1.6;color:#17191c;">{{range .Items}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{range .Sections}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 16px 0;">
{{if .Heading}}<tr><td colspan="2" class="dk-text" style="padding:0 0 6px 0;font-size:13px;line-height:1.4;font-weight:700;color:#17191c;text-transform:uppercase;letter-spacing:.04em;">{{.Heading}}</td></tr>{{end}}
{{range .Rows}}<tr><td class="dk-muted dk-rule" valign="top" style="padding:8px 12px 8px 0;border-top:1px solid #e4e7eb;font-size:14px;line-height:1.5;color:#68707a;word-break:break-word;" width="38%">{{.Label}}</td><td class="dk-text dk-rule" valign="top" align="right" style="padding:8px 0;border-top:1px solid #e4e7eb;font-size:14px;line-height:1.5;color:#17191c;text-align:right;word-break:break-word;">{{.Value}}</td></tr>{{end}}
</table>{{end}}
{{if .ActionOK}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:16px 0 20px 0;"><tr><td align="center" bgcolor="#4338ca" style="background-color:#4338ca;border-radius:8px;"><a href="{{.Action}}" target="_blank" style="display:inline-block;padding:13px 26px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.2;font-weight:600;color:#ffffff;text-decoration:none;border-radius:8px;">{{.ActionLabel}}</a></td></tr></table>
<p class="dk-muted" style="margin:0 0 16px 0;font-size:12px;line-height:1.5;color:#68707a;">If the button does not work, copy this link into your browser:<br><a href="{{.Action}}" style="color:#4338ca;word-break:break-all;">{{.Action}}</a></p>{{end}}
{{range .Links}}<p class="dk-text" style="margin:0 0 8px 0;font-size:14px;line-height:1.5;"><a href="{{.URL}}" style="color:#4338ca;">{{.Label}}</a></p>{{end}}
{{if .Footer}}<p class="dk-muted" style="margin:8px 0 0 0;font-size:13px;line-height:1.5;color:#68707a;">{{.Footer}}</p>{{end}}
</td></tr>
<tr><td align="center" class="dk-muted" style="padding:20px 16px 8px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;line-height:1.6;color:#68707a;">
{{if .Reason}}{{.Reason}}<br>{{end}}{{if .HomeURL}}<a href="{{.HomeURL}}" style="color:#68707a;text-decoration:underline;">Supermetry</a>{{else}}Supermetry{{end}} &middot; privacy-friendly web analytics
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`))

func renderLayout(n Notice) string {
	data := layoutData{Notice: n, HomeURL: baseOf(n.BaseURL)}
	if data.HomeURL != "" {
		data.LogoURL = data.HomeURL + LogoPath
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
