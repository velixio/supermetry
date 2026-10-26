use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use reqwest::header::{HeaderValue, CONTENT_TYPE, ORIGIN, RETRY_AFTER, USER_AGENT};
use serde::Serialize;

use crate::event::{Context, Event};
use crate::Error;

/// The User-Agent sent when no visitor User-Agent is forwarded.
pub const DEFAULT_USER_AGENT: &str = concat!("supermetry-rust/", env!("CARGO_PKG_VERSION"));

/// Path of the public event intake endpoint, relative to `host`.
pub const EVENTS_PATH: &str = "/api/public/v1/events";

const MAX_BODY_BYTES: usize = 8 << 10;
const MAX_PROPS: usize = 30;
const MAX_KEY_BYTES: usize = 300;
const MAX_VALUE_BYTES: usize = 2000;
const MAX_URL_BYTES: usize = 2000;
const MAX_NAME_BYTES: usize = 300;
const MAX_ID_BYTES: usize = 64;
const MAX_PROFILE_ID_CHARS: usize = 128;
const MAX_TRAITS: usize = 20;
const MAX_TRAIT_KEY_CHARS: usize = 64;
const MAX_TRAIT_VALUE_CHARS: usize = 256;
const MAX_RETRY_AFTER: Duration = Duration::from_secs(5);

/// Client configuration. `site` and `host` are required; everything else has
/// a sensible default via [`Default`].
///
/// ```
/// use supermetry::Config;
///
/// let config = Config {
///     site: "your-site-id".into(),
///     host: "https://analytics.supermetry.com".into(),
///     url: Some("https://supermetry.com".into()),
///     ..Default::default()
/// };
/// # let _ = config;
/// ```
#[derive(Debug, Clone)]
pub struct Config {
    /// Public site ID.
    pub site: String,
    /// Base URL of the Supermetry server, e.g. `https://analytics.supermetry.com`.
    pub host: String,
    /// Full endpoint URL override. Defaults to `host + "/api/public/v1/events"`.
    pub endpoint: Option<String>,
    /// Default absolute page URL for events that do not carry one.
    pub url: Option<String>,
    /// User-Agent sent when the caller does not forward the visitor's.
    /// Defaults to [`DEFAULT_USER_AGENT`]. Never use a generic HTTP-client
    /// UA (`curl`, `reqwest`, ...): those are filtered as bots.
    pub user_agent: Option<String>,
    /// When `true`, every call is a successful no-op.
    pub disabled: bool,
    /// Per-request timeout. Default 5 seconds.
    pub timeout: Duration,
    /// Retries on network errors, `429` and `5xx`, reusing the same event ID.
    /// Default 1.
    pub max_retries: u32,
    /// Delay before a retry when the server sends no `Retry-After`.
    /// Default 500 ms. `Retry-After` is honoured up to 5 seconds.
    pub retry_delay: Duration,
    /// Send an `Origin` header derived from the event URL. Default `true`.
    pub send_origin: bool,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            site: String::new(),
            host: String::new(),
            endpoint: None,
            url: None,
            user_agent: None,
            disabled: false,
            timeout: Duration::from_secs(5),
            max_retries: 1,
            retry_delay: Duration::from_millis(500),
            send_origin: true,
        }
    }
}

/// Fluent builder for [`Supermetry`], created by [`Supermetry::builder`].
#[derive(Debug, Clone)]
pub struct SupermetryBuilder {
    config: Config,
}

impl SupermetryBuilder {
    /// Sets the default absolute page URL.
    pub fn url(mut self, url: impl Into<String>) -> Self {
        self.config.url = Some(url.into());
        self
    }

    /// Sets the fallback User-Agent.
    pub fn user_agent(mut self, user_agent: impl Into<String>) -> Self {
        self.config.user_agent = Some(user_agent.into());
        self
    }

    /// Overrides the full endpoint URL.
    pub fn endpoint(mut self, endpoint: impl Into<String>) -> Self {
        self.config.endpoint = Some(endpoint.into());
        self
    }

    /// Disables sending (every call becomes a successful no-op).
    pub fn disabled(mut self, disabled: bool) -> Self {
        self.config.disabled = disabled;
        self
    }

    /// Sets the per-request timeout.
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.config.timeout = timeout;
        self
    }

    /// Sets how many times a failed request is retried.
    pub fn max_retries(mut self, retries: u32) -> Self {
        self.config.max_retries = retries;
        self
    }

    /// Sets the default delay between retries.
    pub fn retry_delay(mut self, delay: Duration) -> Self {
        self.config.retry_delay = delay;
        self
    }

    /// Enables or disables the derived `Origin` header.
    pub fn send_origin(mut self, send: bool) -> Self {
        self.config.send_origin = send;
        self
    }

    /// Builds the client, validating the configuration.
    pub fn build(self) -> Result<Supermetry, Error> {
        Supermetry::new(self.config)
    }
}

/// Visitor traits for [`Supermetry::identify`]: string keys to string values,
/// at most 20 entries, keys ≤ 64 characters, values ≤ 256 characters.
/// Invalid entries are dropped locally instead of failing the whole event.
///
/// Build from arrays, vectors or maps of string pairs, or use `()` /
/// [`Traits::new`] for none.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Traits(pub BTreeMap<String, String>);

impl Traits {
    /// Empty traits.
    pub fn new() -> Self {
        Self::default()
    }

    /// Adds one trait.
    pub fn insert(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.0.insert(key.into(), value.into());
        self
    }
}

impl From<()> for Traits {
    fn from(_: ()) -> Self {
        Traits::default()
    }
}

impl<K: Into<String>, V: Into<String>, const N: usize> From<[(K, V); N]> for Traits {
    fn from(pairs: [(K, V); N]) -> Self {
        Traits(
            pairs
                .into_iter()
                .map(|(k, v)| (k.into(), v.into()))
                .collect(),
        )
    }
}

impl<K: Into<String>, V: Into<String>> From<Vec<(K, V)>> for Traits {
    fn from(pairs: Vec<(K, V)>) -> Self {
        Traits(
            pairs
                .into_iter()
                .map(|(k, v)| (k.into(), v.into()))
                .collect(),
        )
    }
}

impl<K: Into<String>, V: Into<String>> From<BTreeMap<K, V>> for Traits {
    fn from(map: BTreeMap<K, V>) -> Self {
        Traits(map.into_iter().map(|(k, v)| (k.into(), v.into())).collect())
    }
}

impl<K: Into<String>, V: Into<String>, S> From<HashMap<K, V, S>> for Traits {
    fn from(map: HashMap<K, V, S>) -> Self {
        Traits(map.into_iter().map(|(k, v)| (k.into(), v.into())).collect())
    }
}

/// Async Supermetry client. Cheap to clone; share one per process.
///
/// The client is shared across visitors, so it does not remember an
/// identified profile: attach `profile_id` to each event explicitly.
#[derive(Debug, Clone)]
pub struct Supermetry {
    inner: Arc<Inner>,
}

#[derive(Debug)]
struct Inner {
    config: Config,
    endpoint: String,
    http: reqwest::Client,
}

#[derive(Serialize)]
struct Payload<'a> {
    v: u8,
    id: &'a str,
    ts: i64,
    site: &'a str,
    name: &'a str,
    url: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    referrer: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    screen_width: Option<u16>,
    #[serde(skip_serializing_if = "Option::is_none")]
    props: Option<BTreeMap<&'a str, serde_json::Value>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    revenue: Option<RevenuePayload>,
    #[serde(skip_serializing_if = "Option::is_none")]
    profile_id: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    traits: Option<BTreeMap<&'a str, &'a str>>,
}

#[derive(Serialize)]
struct RevenuePayload {
    amount: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    currency: Option<String>,
}

/// A validated request ready to be sent.
#[derive(Debug, Clone)]
pub(crate) struct Prepared {
    pub(crate) body: Vec<u8>,
    pub(crate) user_agent: HeaderValue,
    pub(crate) ip: Option<HeaderValue>,
    pub(crate) origin: Option<HeaderValue>,
}

impl Supermetry {
    /// Creates a client from a [`Config`].
    pub fn new(config: Config) -> Result<Self, Error> {
        if config.site.trim().is_empty() {
            return Err(Error::Config("site is required".into()));
        }
        let endpoint = match &config.endpoint {
            Some(endpoint) => endpoint.clone(),
            None => {
                let host = config.host.trim().trim_end_matches('/');
                if host.is_empty() {
                    return Err(Error::Config("host is required".into()));
                }
                format!("{host}{EVENTS_PATH}")
            }
        };
        match reqwest::Url::parse(&endpoint) {
            Ok(u) if matches!(u.scheme(), "http" | "https") && u.host().is_some() => {}
            _ => return Err(Error::Config("host must be an absolute http(s) URL".into())),
        }
        if let Some(ua) = &config.user_agent {
            HeaderValue::from_str(ua)
                .map_err(|_| Error::Config("user_agent is not a valid header value".into()))?;
        }
        let http = reqwest::Client::builder()
            .timeout(config.timeout)
            .build()
            .map_err(|e| Error::Config(format!("http client: {e}")))?;
        Ok(Supermetry {
            inner: Arc::new(Inner {
                config,
                endpoint,
                http,
            }),
        })
    }

    /// Starts a [`SupermetryBuilder`] with the required `site` and `host`.
    pub fn builder(site: impl Into<String>, host: impl Into<String>) -> SupermetryBuilder {
        SupermetryBuilder {
            config: Config {
                site: site.into(),
                host: host.into(),
                ..Config::default()
            },
        }
    }

    /// The resolved intake endpoint.
    pub fn endpoint(&self) -> &str {
        &self.inner.endpoint
    }

    /// Sends an event and waits for the server to accept it.
    ///
    /// A `202` is returned for accepted events and for events silently
    /// dropped by privacy, bot, exclusion or dedup rules, so `Ok(())` does not
    /// guarantee the event was counted. To fire and forget, spawn the future
    /// and ignore the result.
    pub async fn track(&self, event: Event) -> Result<(), Error> {
        if self.inner.config.disabled {
            return Ok(());
        }
        let prepared = self.prepare(&event, None)?;
        self.send(prepared).await
    }

    /// Sends a `pageview` for the context URL (or the default `url`).
    pub async fn pageview(&self, context: Context) -> Result<(), Error> {
        self.track(Event::new("pageview").context(context)).await
    }

    /// Sends an `identify` event linking the visitor to `profile_id` and
    /// storing `traits` on the profile. Invalid traits are dropped locally.
    ///
    /// Profiles must be enabled for the site; otherwise the server accepts
    /// and discards the event.
    pub async fn identify(
        &self,
        profile_id: impl Into<String>,
        traits: impl Into<Traits>,
        context: Context,
    ) -> Result<(), Error> {
        if self.inner.config.disabled {
            return Ok(());
        }
        let event = Event::new("identify")
            .profile_id(profile_id)
            .context(context);
        let prepared = self.prepare(&event, Some(traits.into()))?;
        self.send(prepared).await
    }

    /// Validates an event and serializes the request. `traits` is only
    /// passed for `identify`.
    pub(crate) fn prepare(&self, event: &Event, traits: Option<Traits>) -> Result<Prepared, Error> {
        let config = &self.inner.config;
        let invalid = |msg: &str| Error::Invalid(msg.to_owned());

        if event.name.is_empty() || event.name.len() > MAX_NAME_BYTES {
            return Err(invalid("name must be 1-300 bytes"));
        }
        if traits.is_some() && event.name != "identify" {
            return Err(invalid("traits are only allowed on identify"));
        }
        if traits.is_none() && event.name == "identify" {
            return Err(invalid("send identify events with Supermetry::identify"));
        }

        let url = event
            .context
            .url
            .as_deref()
            .or(config.url.as_deref())
            .ok_or_else(|| invalid("url is required (per event or as the client default)"))?;
        if url.len() > MAX_URL_BYTES {
            return Err(invalid("url exceeds 2000 bytes"));
        }
        let parsed = reqwest::Url::parse(url).map_err(|_| invalid("url must be absolute"))?;
        if !matches!(parsed.scheme(), "http" | "https") || parsed.host().is_none() {
            return Err(invalid("url must be an absolute http(s) URL"));
        }
        if !parsed.username().is_empty() || parsed.password().is_some() {
            return Err(invalid("url must not contain userinfo"));
        }

        let referrer = event
            .context
            .referrer
            .as_deref()
            .filter(|r| !r.is_empty() && r.len() <= MAX_VALUE_BYTES);

        if let Some(width) = event.screen_width {
            if width > i16::MAX as u16 {
                return Err(invalid("screen_width must be 0-32767"));
            }
        }

        let props: BTreeMap<&str, serde_json::Value> = event
            .props
            .iter()
            .filter(|(k, v)| {
                !k.is_empty() && k.len() <= MAX_KEY_BYTES && v.byte_len() <= MAX_VALUE_BYTES
            })
            .filter_map(|(k, v)| Some((k.as_str(), v.to_json()?)))
            .take(MAX_PROPS)
            .collect();

        let revenue = match &event.revenue {
            Some(revenue) if event.name != "pageview" => {
                let (amount, currency) = revenue.normalize().map_err(Error::Invalid)?;
                Some(RevenuePayload { amount, currency })
            }
            _ => None,
        };

        let profile_id = event.profile_id.as_deref();
        if let Some(pid) = profile_id {
            let chars = pid.chars().count();
            if chars == 0 || chars > MAX_PROFILE_ID_CHARS {
                return Err(invalid("profile_id must be 1-128 characters"));
            }
        } else if event.name == "identify" {
            return Err(invalid("identify requires a profile_id"));
        }

        let traits_map: Option<BTreeMap<&str, &str>> = traits.as_ref().map(|t| {
            t.0.iter()
                .filter(|(k, v)| {
                    let kc = k.chars().count();
                    kc > 0
                        && kc <= MAX_TRAIT_KEY_CHARS
                        && v.chars().count() <= MAX_TRAIT_VALUE_CHARS
                })
                .take(MAX_TRAITS)
                .map(|(k, v)| (k.as_str(), v.as_str()))
                .collect()
        });

        let generated;
        let id = match &event.id {
            Some(id) => {
                if id.is_empty()
                    || id.len() > MAX_ID_BYTES
                    || !id
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
                {
                    return Err(invalid("id must be 1-64 chars of [A-Za-z0-9_-]"));
                }
                id.as_str()
            }
            None => {
                generated = uuid::Uuid::new_v4().simple().to_string();
                generated.as_str()
            }
        };
        let ts = event.timestamp_ms.unwrap_or_else(now_ms);

        let payload = Payload {
            v: 1,
            id,
            ts,
            site: &config.site,
            name: &event.name,
            url,
            referrer,
            screen_width: event.screen_width,
            props: if props.is_empty() { None } else { Some(props) },
            revenue,
            profile_id,
            traits: traits_map.filter(|t| !t.is_empty()),
        };
        let body = serde_json::to_vec(&payload).map_err(|e| Error::Invalid(e.to_string()))?;
        if body.len() > MAX_BODY_BYTES {
            return Err(invalid("payload exceeds 8 KB"));
        }

        let ua = event
            .context
            .user_agent
            .as_deref()
            .filter(|ua| !ua.trim().is_empty())
            .or(config.user_agent.as_deref())
            .unwrap_or(DEFAULT_USER_AGENT);
        let user_agent = HeaderValue::from_str(ua)
            .map_err(|_| invalid("user_agent is not a valid header value"))?;
        let ip = match event
            .context
            .ip
            .as_deref()
            .filter(|ip| !ip.trim().is_empty())
        {
            Some(ip) => Some(
                HeaderValue::from_str(ip.trim())
                    .map_err(|_| invalid("ip is not a valid header value"))?,
            ),
            None => None,
        };
        let origin = if config.send_origin {
            HeaderValue::from_str(&parsed.origin().ascii_serialization()).ok()
        } else {
            None
        };
        Ok(Prepared {
            body,
            user_agent,
            ip,
            origin,
        })
    }

    async fn send(&self, prepared: Prepared) -> Result<(), Error> {
        let config = &self.inner.config;
        let mut attempt = 0;
        loop {
            let mut request = self
                .inner
                .http
                .post(&self.inner.endpoint)
                .header(CONTENT_TYPE, "application/json")
                .header(USER_AGENT, prepared.user_agent.clone())
                .body(prepared.body.clone());
            if let Some(ip) = &prepared.ip {
                request = request.header("X-Forwarded-For", ip.clone());
            }
            if let Some(origin) = &prepared.origin {
                request = request.header(ORIGIN, origin.clone());
            }
            let can_retry = attempt < config.max_retries;
            attempt += 1;
            match request.send().await {
                Ok(response) => {
                    let status = response.status();
                    if status.is_success() {
                        return Ok(());
                    }
                    let retryable = status.as_u16() == 429 || status.is_server_error();
                    if !(retryable && can_retry) {
                        return Err(Error::Status(status.as_u16()));
                    }
                    let delay = response
                        .headers()
                        .get(RETRY_AFTER)
                        .and_then(|v| v.to_str().ok())
                        .and_then(|v| v.trim().parse::<u64>().ok())
                        .map(|secs| Duration::from_secs(secs).min(MAX_RETRY_AFTER))
                        .unwrap_or(config.retry_delay);
                    tokio::time::sleep(delay).await;
                }
                Err(err) => {
                    if !can_retry || err.is_builder() {
                        return Err(Error::Http(err));
                    }
                    tokio::time::sleep(config.retry_delay).await;
                }
            }
        }
    }
}

fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Event;

    fn client() -> Supermetry {
        Supermetry::builder("site_1", "https://analytics.supermetry.com/")
            .build()
            .unwrap()
    }

    fn json(prepared: &Prepared) -> serde_json::Value {
        serde_json::from_slice(&prepared.body).unwrap()
    }

    #[test]
    fn endpoint_from_host() {
        assert_eq!(
            client().endpoint(),
            "https://analytics.supermetry.com/api/public/v1/events"
        );
    }

    #[test]
    fn config_requires_site_and_host() {
        assert!(matches!(
            Supermetry::builder("", "https://a.example").build(),
            Err(Error::Config(_))
        ));
        assert!(matches!(
            Supermetry::builder("s", "").build(),
            Err(Error::Config(_))
        ));
        assert!(matches!(
            Supermetry::builder("s", "ftp://a.example").build(),
            Err(Error::Config(_))
        ));
    }

    #[test]
    fn payload_shape() {
        let event = Event::new("Signup")
            .url("https://supermetry.com/signup")
            .referrer("https://google.com/")
            .prop("plan", "pro")
            .prop("seats", 5)
            .prop("ratio", 0.5)
            .prop("trial", true)
            .revenue(49.9, "USD")
            .profile_id("user_1")
            .screen_width(1280);
        let prepared = client().prepare(&event, None).unwrap();
        let body = json(&prepared);
        assert_eq!(body["v"], 1);
        assert_eq!(body["site"], "site_1");
        assert_eq!(body["name"], "Signup");
        assert_eq!(body["url"], "https://supermetry.com/signup");
        assert_eq!(body["referrer"], "https://google.com/");
        assert_eq!(body["screen_width"], 1280);
        assert_eq!(body["props"]["plan"], "pro");
        assert_eq!(body["props"]["seats"], 5);
        assert_eq!(body["props"]["ratio"], 0.5);
        assert_eq!(body["props"]["trial"], true);
        assert_eq!(body["revenue"]["amount"], "49.9");
        assert_eq!(body["revenue"]["currency"], "USD");
        assert_eq!(body["profile_id"], "user_1");
        assert!(body.get("traits").is_none());
        let id = body["id"].as_str().unwrap();
        assert_eq!(id.len(), 32);
        assert!(id.bytes().all(|b| b.is_ascii_hexdigit()));
        let ts = body["ts"].as_i64().unwrap();
        assert!((ts - now_ms()).abs() < 10_000);
        assert_eq!(prepared.user_agent, DEFAULT_USER_AGENT);
        assert_eq!(prepared.origin.unwrap(), "https://supermetry.com");
        assert!(prepared.ip.is_none());
    }

    #[test]
    fn forwards_visitor_ua_and_ip() {
        let event = Event::pageview("https://supermetry.com/")
            .user_agent("Mozilla/5.0 (X11; Linux x86_64)")
            .ip("203.0.113.7");
        let prepared = client().prepare(&event, None).unwrap();
        assert_eq!(prepared.user_agent, "Mozilla/5.0 (X11; Linux x86_64)");
        assert_eq!(prepared.ip.unwrap(), "203.0.113.7");
    }

    #[test]
    fn default_url_and_missing_url() {
        let c = Supermetry::builder("s", "https://a.example")
            .url("https://supermetry.com/")
            .build()
            .unwrap();
        let body = json(&c.prepare(&Event::new("x"), None).unwrap());
        assert_eq!(body["url"], "https://supermetry.com/");
        assert!(matches!(
            client().prepare(&Event::new("x"), None),
            Err(Error::Invalid(_))
        ));
    }

    #[test]
    fn rejects_bad_urls() {
        for url in [
            "/relative",
            "ftp://supermetry.com/",
            "https://user:pw@example.com/",
            "mailto:a@example.com",
        ] {
            let res = client().prepare(&Event::new("x").url(url), None);
            assert!(matches!(res, Err(Error::Invalid(_))), "{url}");
        }
        let long = format!("https://supermetry.com/{}", "a".repeat(2000));
        assert!(client().prepare(&Event::new("x").url(long), None).is_err());
    }

    #[test]
    fn rejects_bad_name_and_profile() {
        let url = "https://supermetry.com/";
        assert!(client().prepare(&Event::new("").url(url), None).is_err());
        assert!(client()
            .prepare(&Event::new("x".repeat(301)).url(url), None)
            .is_err());
        assert!(client()
            .prepare(&Event::new("x").url(url).profile_id(""), None)
            .is_err());
        assert!(client()
            .prepare(&Event::new("x").url(url).profile_id("é".repeat(128)), None)
            .is_ok());
        assert!(client()
            .prepare(&Event::new("x").url(url).profile_id("é".repeat(129)), None)
            .is_err());
        assert!(client()
            .prepare(&Event::new("x").url(url).id("bad id"), None)
            .is_err());
    }

    #[test]
    fn traits_only_on_identify() {
        let url = "https://supermetry.com/";
        let res = client().prepare(
            &Event::new("Signup").url(url).profile_id("u"),
            Some(Traits::new().insert("a", "b")),
        );
        assert!(matches!(res, Err(Error::Invalid(_))));
        let res = client().prepare(&Event::new("identify").url(url).profile_id("u"), None);
        assert!(matches!(res, Err(Error::Invalid(_))));
    }

    #[test]
    fn invalid_traits_dropped_and_capped() {
        let mut traits = Traits::new()
            .insert("", "empty key")
            .insert("k".repeat(65), "long key")
            .insert("long_value", "v".repeat(257));
        for i in 0..25 {
            traits = traits.insert(format!("t{i:02}"), "ok");
        }
        let event = Event::new("identify")
            .url("https://supermetry.com/")
            .profile_id("u");
        let body = json(&client().prepare(&event, Some(traits)).unwrap());
        let traits = body["traits"].as_object().unwrap();
        assert_eq!(traits.len(), 20);
        assert!(traits.keys().all(|k| k.starts_with('t')));
    }

    #[test]
    fn props_truncated_and_invalid_dropped() {
        let mut event = Event::new("x")
            .url("https://supermetry.com/")
            .prop("", "empty")
            .prop("k".repeat(301), "long key")
            .prop("big", "v".repeat(2001))
            .prop("nan", f64::NAN);
        for i in 0..40 {
            event = event.prop(format!("p{i:02}"), i);
        }
        let body = json(&client().prepare(&event, None).unwrap());
        let props = body["props"].as_object().unwrap();
        assert_eq!(props.len(), 30);
        assert!(props.contains_key("p00") && props.contains_key("p29"));
        assert!(!props.contains_key("p30"));
    }

    #[test]
    fn revenue_validation() {
        let url = "https://supermetry.com/";
        let ok = |amount: crate::Amount, cur: &str| {
            client()
                .prepare(&Event::new("buy").url(url).revenue(amount, cur), None)
                .map(|p| json(&p)["revenue"].clone())
        };
        assert_eq!(ok("49.90".into(), "EUR").unwrap()["amount"], "49.90");
        assert_eq!(ok((0.1 + 0.2).into(), "").unwrap()["amount"], "0.3");
        assert!(ok((0.1 + 0.2).into(), "")
            .unwrap()
            .get("currency")
            .is_none());
        assert_eq!(ok(100.into(), "USD").unwrap()["amount"], "100");
        assert!(ok("1.1234567".into(), "USD").is_err());
        assert!(ok("0".into(), "USD").is_err());
        assert!(ok((-1.0).into(), "USD").is_err());
        assert!(ok("1".into(), "usd").is_err());
        let pv = Event::pageview(url).revenue("1", "USD");
        assert!(json(&client().prepare(&pv, None).unwrap())
            .get("revenue")
            .is_none());
    }

    #[test]
    fn body_limit() {
        let mut event = Event::new("x").url("https://supermetry.com/");
        for i in 0..10 {
            event = event.prop(format!("p{i}"), "v".repeat(1000));
        }
        assert!(matches!(
            client().prepare(&event, None),
            Err(Error::Invalid(_))
        ));
    }

    #[test]
    fn custom_id_and_ts_kept() {
        let event = Event::new("x")
            .url("https://supermetry.com/")
            .id("abc_123")
            .timestamp_ms(1_700_000_000_000);
        let body = json(&client().prepare(&event, None).unwrap());
        assert_eq!(body["id"], "abc_123");
        assert_eq!(body["ts"], 1_700_000_000_000_i64);
    }
}
