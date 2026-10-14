use std::fmt;

/// Errors returned by the Supermetry client.
#[derive(Debug)]
#[non_exhaustive]
pub enum Error {
    /// The client configuration is invalid (missing site, bad host, ...).
    Config(String),
    /// The event failed local validation and was not sent.
    Invalid(String),
    /// The request could not be sent or the response could not be read.
    Http(reqwest::Error),
    /// The server answered with a non-success status code.
    ///
    /// `400` means the payload was rejected (for example because the URL's
    /// host is not an allowed host of the site); `429` and `5xx` are reported
    /// after the retry budget has been spent.
    Status(u16),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Error::Config(msg) => write!(f, "invalid supermetry config: {msg}"),
            Error::Invalid(msg) => write!(f, "invalid supermetry event: {msg}"),
            Error::Http(err) => write!(f, "supermetry request failed: {err}"),
            Error::Status(status) => write!(f, "supermetry server answered HTTP {status}"),
        }
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Error::Http(err) => Some(err),
            _ => None,
        }
    }
}

impl From<reqwest::Error> for Error {
    fn from(err: reqwest::Error) -> Self {
        Error::Http(err)
    }
}
