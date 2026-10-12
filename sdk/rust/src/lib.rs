//! Server-side SDK for [Supermetry](https://supermetry.com), self-hosted web
//! analytics.
//!
//! Events are sent to `POST {host}/api/public/v1/events`. Forward the
//! visitor's real `User-Agent` and IP with every event: Supermetry derives
//! visitors from IP + User-Agent, and generic HTTP-client User-Agents are
//! filtered as bots. Sites with *strict origin* enabled should turn it off
//! for server-sent events.
//!
//! ```no_run
//! use supermetry::{Context, Event, Supermetry};
//!
//! # async fn run() -> Result<(), supermetry::Error> {
//! let client = Supermetry::builder("your-site-id", "https://analytics.example.com")
//!     .url("https://example.com")
//!     .build()?;
//!
//! client
//!     .track(
//!         Event::new("Signup")
//!             .url("https://example.com/signup")
//!             .prop("plan", "pro")
//!             .revenue("49.90", "USD")
//!             .user_agent("Mozilla/5.0 ...")
//!             .ip("203.0.113.7"),
//!     )
//!     .await?;
//!
//! client
//!     .identify(
//!         "user_123",
//!         [("plan", "pro")],
//!         Context::new().user_agent("Mozilla/5.0 ...").ip("203.0.113.7"),
//!     )
//!     .await?;
//! # Ok(())
//! # }
//! ```
#![warn(missing_docs)]

mod client;
mod error;
mod event;

pub use client::{Config, Supermetry, SupermetryBuilder, Traits, DEFAULT_USER_AGENT, EVENTS_PATH};
pub use error::Error;
pub use event::{Amount, Context, Event, PropValue, Revenue};
