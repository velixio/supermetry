package installer

import (
	"fmt"
	"net/url"
)

// adoptDotEnv offers the settings of the opts.DotEnv file as installer answers.
// It fills only fields that no flag set and shows the values with secrets
// masked. It does nothing without opts.DotEnv, and leaves opts untouched when
// the user declines or the file has no database URL.
func adoptDotEnv(opts Options, p *Prompter) (Options, error) {
	if opts.DotEnv == "" {
		return opts, nil
	}
	values := readDotEnv(opts.DotEnv)
	dbURL := values["SUPERMETRY_DATABASE_URL"]
	if dbURL == "" || opts.DB.URL != "" || opts.DB.Host != "" {
		return opts, nil
	}
	fmt.Fprintf(p.out, "Found configuration in %s:\n", opts.DotEnv)
	fmt.Fprintf(p.out, "  database:   %s\n", maskDatabaseURL(dbURL))
	if v := values["SUPERMETRY_BASE_URL"]; v != "" {
		fmt.Fprintf(p.out, "  public URL: %s\n", v)
	}
	if v := values["SUPERMETRY_LISTEN"]; v != "" {
		fmt.Fprintf(p.out, "  listen:     %s\n", v)
	}
	if values["SUPERMETRY_SECRET_KEY"] != "" {
		fmt.Fprintln(p.out, "  secret key: set (hidden)")
	}
	use, err := p.Confirm("Use this configuration?", true)
	if err != nil || !use {
		return opts, err
	}
	opts.DB.URL = dbURL
	if opts.BaseURL == "" {
		opts.BaseURL = values["SUPERMETRY_BASE_URL"]
	}
	if opts.Listen == "" {
		opts.Listen = values["SUPERMETRY_LISTEN"]
	}
	return opts, nil
}

func maskDatabaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "****")
	}
	return u.String()
}
