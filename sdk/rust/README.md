# supermetry (Rust)

Async server-side SDK for [Supermetry](https://supermetry.com), self-hosted web analytics.
Sends events to `POST {host}/api/public/v1/events` using `reqwest` (rustls).

## Install

```toml
[dependencies]
supermetry = "0.1"
tokio = { version = "1", features = ["macros", "rt-multi-thread"] }
```

## Quick start

```rust
use supermetry::{Context, Event, Supermetry};

#[tokio::main]
async fn main() -> Result<(), supermetry::Error> {
    let client = Supermetry::builder("your-site-id", "https://analytics.supermetry.com")
        .url("https://supermetry.com") // default page URL for events without one
        .build()?;

    // Take these from the incoming request.
    let ua = "Mozilla/5.0 (...)";
    let ip = "203.0.113.7";

    client
        .pageview(Context::new().url("https://supermetry.com/pricing").user_agent(ua).ip(ip))
        .await?;

    client
        .track(
            Event::new("Purchase")
                .url("https://supermetry.com/checkout")
                .prop("plan", "pro")
                .prop("seats", 5)
                .prop("annual", true)
                .revenue("490.00", "USD")
                .profile_id("user_123")
                .user_agent(ua)
                .ip(ip),
        )
        .await?;

    client
        .identify("user_123", [("plan", "pro")], Context::new().user_agent(ua).ip(ip))
        .await?;
    Ok(())
}
```

`Config` can be used instead of the builder:

```rust
use supermetry::{Config, Supermetry};

let client = Supermetry::new(Config {
    site: "your-site-id".into(),
    host: "https://analytics.supermetry.com".into(),
    url: Some("https://supermetry.com".into()),
    ..Default::default()
})?;
```

Fire and forget by spawning the future and ignoring its result:

```rust
let c = client.clone();
tokio::spawn(async move { let _ = c.track(event).await; });
```

See `examples/basic.rs` (`cargo run --example basic`).

## Forward the visitor's User-Agent and IP

Supermetry derives visitors from IP + User-Agent, so pass the visitor's real values with
every event (`.user_agent(..)` / `.ip(..)` on `Event`, or the same on `Context`). The IP is
sent as `X-Forwarded-For`, which the server only trusts from addresses listed in
`SUPERMETRY_TRUSTED_PROXIES`, so add your application servers there. Without a forwarded
UA the SDK sends `supermetry-rust/0.1.0`; never send a generic client UA such as `curl` or
`reqwest`, since those are filtered as bots.

## Strict origin

Turn *strict origin* **off** for sites that receive server-sent events. The SDK sends an
`Origin` derived from the event URL (disable with `.send_origin(false)`), but server-side
traffic should not rely on origin checks.

## Behaviour

- `track`, `pageview` and `identify` return `Result<(), supermetry::Error>`. A `202` is
  returned for accepted events and for events the server silently drops (bots, privacy
  signals, exclusions, duplicates).
- Each event gets `v: 1`, an `id` (UUID without dashes) and `ts` (Unix ms). Network errors,
  `429` (honouring `Retry-After` up to 5 s) and `5xx` are retried once with the same `id`,
  so the server deduplicates. `400` is not retried.
- Local validation: name ≤ 300 bytes; `url` required, absolute `http(s)`, ≤ 2000 bytes,
  no userinfo; `profile_id` 1–128 chars; revenue a positive amount with ≤ 6 decimals and an
  uppercase ISO 4217 currency; body ≤ 8 KB. Invalid props (empty/long keys, values over
  2000 bytes, non-finite floats) are dropped and props are capped at 30 keys. Traits are
  only accepted by `identify`; invalid traits are dropped and capped at 20.
- The client is shared across visitors and does not remember profile IDs: attach
  `.profile_id(..)` to each event of an identified user.
- `disabled(true)` turns every call into a no-op.

## Development

```sh
cargo test
cargo clippy --all-targets -- -D warnings
cargo fmt --check
```

License: Apache-2.0
