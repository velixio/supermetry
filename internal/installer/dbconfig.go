package installer

import (
	"net"
	"net/url"
)

// DBConfig contains either a complete PostgreSQL URL or its component parts.
type DBConfig struct {
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// ConnString returns a PostgreSQL connection URL with credentials escaped.
func (c DBConfig) ConnString() string {
	if c.URL != "" {
		return c.URL
	}
	sslmode := c.SSLMode
	if sslmode == "" {
		sslmode = "require"
		if c.Host == "localhost" || c.Host == "127.0.0.1" || c.Host == "::1" {
			sslmode = "disable"
		}
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     "/" + c.Name,
		RawQuery: "sslmode=" + url.QueryEscape(sslmode),
	}
	return u.String()
}
