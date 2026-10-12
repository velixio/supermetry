use std::collections::BTreeMap;

/// A custom property value: a string, a number or a boolean.
///
/// The server stores every property as a string, so numbers and booleans are
/// stringified on arrival. Build values with `.into()` from `&str`, `String`,
/// any integer type, `f32`/`f64` or `bool`.
#[derive(Debug, Clone, PartialEq)]
pub enum PropValue {
    /// A string value (≤ 2000 bytes).
    String(String),
    /// A signed integer.
    Int(i64),
    /// An unsigned integer.
    UInt(u64),
    /// A floating point number. Non-finite values are dropped before sending.
    Float(f64),
    /// A boolean.
    Bool(bool),
}

impl PropValue {
    pub(crate) fn to_json(&self) -> Option<serde_json::Value> {
        Some(match self {
            PropValue::String(s) => serde_json::Value::String(s.clone()),
            PropValue::Int(n) => serde_json::Value::from(*n),
            PropValue::UInt(n) => serde_json::Value::from(*n),
            PropValue::Float(n) => serde_json::Value::Number(serde_json::Number::from_f64(*n)?),
            PropValue::Bool(b) => serde_json::Value::Bool(*b),
        })
    }

    /// Size in bytes of the value as the server will store it.
    pub(crate) fn byte_len(&self) -> usize {
        match self {
            PropValue::String(s) => s.len(),
            other => other.to_json().map_or(0, |v| v.to_string().len()),
        }
    }
}

impl From<&str> for PropValue {
    fn from(value: &str) -> Self {
        PropValue::String(value.to_owned())
    }
}

impl From<String> for PropValue {
    fn from(value: String) -> Self {
        PropValue::String(value)
    }
}

impl From<&String> for PropValue {
    fn from(value: &String) -> Self {
        PropValue::String(value.clone())
    }
}

impl From<bool> for PropValue {
    fn from(value: bool) -> Self {
        PropValue::Bool(value)
    }
}

impl From<f64> for PropValue {
    fn from(value: f64) -> Self {
        PropValue::Float(value)
    }
}

impl From<f32> for PropValue {
    fn from(value: f32) -> Self {
        PropValue::Float(f64::from(value))
    }
}

macro_rules! prop_from_int {
    ($variant:ident, $wide:ty: $($t:ty),*) => {$(
        impl From<$t> for PropValue {
            fn from(value: $t) -> Self {
                PropValue::$variant(<$wide>::from(value))
            }
        }
    )*};
}

prop_from_int!(Int, i64: i8, i16, i32, i64);
prop_from_int!(UInt, u64: u8, u16, u32, u64);

impl From<isize> for PropValue {
    fn from(value: isize) -> Self {
        PropValue::Int(value as i64)
    }
}

impl From<usize> for PropValue {
    fn from(value: usize) -> Self {
        PropValue::UInt(value as u64)
    }
}

/// A revenue amount: either an exact decimal string (`"49.90"`) or a number.
///
/// Numbers are rounded to 6 decimal places (the server's precision) and sent
/// as decimal strings, so float noise such as `0.30000000000000004` does not
/// invalidate the revenue.
#[derive(Debug, Clone, PartialEq)]
pub enum Amount {
    /// An exact decimal string, e.g. `"49.90"`.
    Decimal(String),
    /// A floating point amount.
    Number(f64),
}

impl From<&str> for Amount {
    fn from(value: &str) -> Self {
        Amount::Decimal(value.to_owned())
    }
}

impl From<String> for Amount {
    fn from(value: String) -> Self {
        Amount::Decimal(value)
    }
}

impl From<f64> for Amount {
    fn from(value: f64) -> Self {
        Amount::Number(value)
    }
}

impl From<f32> for Amount {
    fn from(value: f32) -> Self {
        Amount::Number(f64::from(value))
    }
}

macro_rules! amount_from_int {
    ($($t:ty),*) => {$(
        impl From<$t> for Amount {
            fn from(value: $t) -> Self {
                Amount::Decimal(value.to_string())
            }
        }
    )*};
}

amount_from_int!(i32, i64, u32, u64, usize);

/// Revenue attached to a custom event. Ignored by the server on `pageview`.
#[derive(Debug, Clone, PartialEq)]
pub struct Revenue {
    /// The positive amount, at most 6 decimals.
    pub amount: Amount,
    /// ISO 4217 currency code (e.g. `"USD"`). Must match the site currency;
    /// `None` uses the site currency.
    pub currency: Option<String>,
}

impl Revenue {
    /// Creates a revenue value with an explicit currency.
    pub fn new(amount: impl Into<Amount>, currency: impl Into<String>) -> Self {
        let currency = currency.into();
        Revenue {
            amount: amount.into(),
            currency: if currency.is_empty() {
                None
            } else {
                Some(currency)
            },
        }
    }

    /// Validates the revenue and returns `(amount, currency)` ready to send.
    pub(crate) fn normalize(&self) -> Result<(String, Option<String>), String> {
        let amount = match &self.amount {
            Amount::Decimal(s) => {
                let s = s.trim();
                if !valid_decimal(s) {
                    return Err(
                        "revenue amount must be a positive decimal with at most 6 decimals".into(),
                    );
                }
                s.to_owned()
            }
            Amount::Number(n) => {
                if !n.is_finite() || *n <= 0.0 || *n > 1e12 {
                    return Err("revenue amount must be a positive finite number".into());
                }
                let formatted = format!("{n:.6}");
                let trimmed = formatted.trim_end_matches('0').trim_end_matches('.');
                if !valid_decimal(trimmed) {
                    return Err("revenue amount rounds to zero".into());
                }
                trimmed.to_owned()
            }
        };
        if let Some(currency) = &self.currency {
            if currency.len() != 3 || !currency.bytes().all(|b| b.is_ascii_uppercase()) {
                return Err("revenue currency must be a 3-letter uppercase ISO 4217 code".into());
            }
        }
        Ok((amount, self.currency.clone()))
    }
}

fn valid_decimal(s: &str) -> bool {
    let (int, frac) = match s.split_once('.') {
        Some((i, f)) => (i, Some(f)),
        None => (s, None),
    };
    if int.is_empty() || !int.bytes().all(|b| b.is_ascii_digit()) {
        return false;
    }
    if let Some(frac) = frac {
        if frac.is_empty() || frac.len() > 6 || !frac.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
    }
    s.bytes().any(|b| (b'1'..=b'9').contains(&b))
}

/// Per-request visitor context: the page URL plus the visitor's forwarded
/// User-Agent, IP and referrer.
///
/// Forward the visitor's real `User-Agent` and IP: Supermetry derives visitors
/// from IP + User-Agent, and without them every event looks like one visitor
/// coming from your server.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Context {
    /// Absolute page URL. Falls back to the client's default `url`.
    pub url: Option<String>,
    /// The visitor's `User-Agent`, sent as the request `User-Agent` header.
    pub user_agent: Option<String>,
    /// The visitor's IP, sent as `X-Forwarded-For`.
    pub ip: Option<String>,
    /// The referrer of the page view.
    pub referrer: Option<String>,
}

impl Context {
    /// Creates an empty context.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the absolute page URL.
    pub fn url(mut self, url: impl Into<String>) -> Self {
        self.url = Some(url.into());
        self
    }

    /// Forwards the visitor's User-Agent.
    pub fn user_agent(mut self, user_agent: impl Into<String>) -> Self {
        self.user_agent = Some(user_agent.into());
        self
    }

    /// Forwards the visitor's IP address.
    pub fn ip(mut self, ip: impl Into<String>) -> Self {
        self.ip = Some(ip.into());
        self
    }

    /// Sets the referrer.
    pub fn referrer(mut self, referrer: impl Into<String>) -> Self {
        self.referrer = Some(referrer.into());
        self
    }
}

/// An analytics event, built fluently and sent with
/// [`Supermetry::track`](crate::Supermetry::track).
///
/// ```
/// use supermetry::Event;
///
/// let event = Event::new("Signup")
///     .url("https://example.com/signup")
///     .prop("plan", "pro")
///     .prop("seats", 5)
///     .prop("trial", true)
///     .revenue("49.90", "USD")
///     .profile_id("user_123")
///     .user_agent("Mozilla/5.0 ...")
///     .ip("203.0.113.7");
/// assert_eq!(event.name(), "Signup");
/// ```
#[derive(Debug, Clone, PartialEq)]
pub struct Event {
    pub(crate) name: String,
    pub(crate) props: BTreeMap<String, PropValue>,
    pub(crate) revenue: Option<Revenue>,
    pub(crate) profile_id: Option<String>,
    pub(crate) screen_width: Option<u16>,
    pub(crate) id: Option<String>,
    pub(crate) timestamp_ms: Option<i64>,
    pub(crate) context: Context,
}

impl Event {
    /// Creates an event with the given name. `pageview` and `identify` are
    /// reserved names; prefer [`Event::pageview`] and
    /// [`Supermetry::identify`](crate::Supermetry::identify) for those.
    pub fn new(name: impl Into<String>) -> Self {
        Event {
            name: name.into(),
            props: BTreeMap::new(),
            revenue: None,
            profile_id: None,
            screen_width: None,
            id: None,
            timestamp_ms: None,
            context: Context::default(),
        }
    }

    /// Creates a `pageview` event for the given absolute URL.
    pub fn pageview(url: impl Into<String>) -> Self {
        Event::new("pageview").url(url)
    }

    /// The event name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Sets the absolute page URL (falls back to the client's default `url`).
    pub fn url(mut self, url: impl Into<String>) -> Self {
        self.context.url = Some(url.into());
        self
    }

    /// Adds one custom property. Later values replace earlier ones.
    pub fn prop(mut self, key: impl Into<String>, value: impl Into<PropValue>) -> Self {
        self.props.insert(key.into(), value.into());
        self
    }

    /// Adds several custom properties.
    pub fn props<K, V, I>(mut self, props: I) -> Self
    where
        K: Into<String>,
        V: Into<PropValue>,
        I: IntoIterator<Item = (K, V)>,
    {
        for (key, value) in props {
            self.props.insert(key.into(), value.into());
        }
        self
    }

    /// Attaches revenue. Pass an empty currency to use the site currency.
    pub fn revenue(mut self, amount: impl Into<Amount>, currency: impl Into<String>) -> Self {
        self.revenue = Some(Revenue::new(amount, currency));
        self
    }

    /// Attaches the identified profile ID (1–128 characters).
    pub fn profile_id(mut self, profile_id: impl Into<String>) -> Self {
        self.profile_id = Some(profile_id.into());
        self
    }

    /// Forwards the visitor's User-Agent.
    pub fn user_agent(mut self, user_agent: impl Into<String>) -> Self {
        self.context.user_agent = Some(user_agent.into());
        self
    }

    /// Forwards the visitor's IP address as `X-Forwarded-For`.
    pub fn ip(mut self, ip: impl Into<String>) -> Self {
        self.context.ip = Some(ip.into());
        self
    }

    /// Sets the referrer.
    pub fn referrer(mut self, referrer: impl Into<String>) -> Self {
        self.context.referrer = Some(referrer.into());
        self
    }

    /// Sets the screen width in CSS pixels (0–32767).
    pub fn screen_width(mut self, width: u16) -> Self {
        self.screen_width = Some(width);
        self
    }

    /// Replaces the whole visitor context (URL, User-Agent, IP, referrer).
    pub fn context(mut self, context: Context) -> Self {
        self.context = context;
        self
    }

    /// Overrides the event ID (dedup key, 1–64 chars of `[A-Za-z0-9_-]`).
    /// By default a random UUID without dashes is generated per send.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Overrides the event timestamp in Unix milliseconds (defaults to now).
    pub fn timestamp_ms(mut self, ts: i64) -> Self {
        self.timestamp_ms = Some(ts);
        self
    }
}
