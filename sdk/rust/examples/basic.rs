//! Run with:
//!
//! ```sh
//! SUPERMETRY_SITE=your-site-id SUPERMETRY_HOST=https://analytics.example.com \
//!     cargo run --example basic
//! ```

use supermetry::{Context, Event, Supermetry};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let site = std::env::var("SUPERMETRY_SITE").unwrap_or_else(|_| "your-site-id".into());
    let host = std::env::var("SUPERMETRY_HOST").unwrap_or_else(|_| "http://localhost:8080".into());

    let client = Supermetry::builder(site, host)
        .url("https://example.com")
        .build()?;

    // In a web handler, take these from the incoming request.
    let visitor_ua = "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0";
    let visitor_ip = "203.0.113.7";

    client
        .pageview(
            Context::new()
                .url("https://example.com/pricing")
                .user_agent(visitor_ua)
                .ip(visitor_ip),
        )
        .await?;

    client
        .identify(
            "user_123",
            [("plan", "pro"), ("company", "Acme")],
            Context::new().user_agent(visitor_ua).ip(visitor_ip),
        )
        .await?;

    client
        .track(
            Event::new("Purchase")
                .url("https://example.com/checkout")
                .prop("plan", "pro")
                .prop("seats", 5)
                .prop("annual", true)
                .revenue("490.00", "USD")
                .profile_id("user_123")
                .user_agent(visitor_ua)
                .ip(visitor_ip),
        )
        .await?;

    // Fire and forget: spawn the future and ignore its result.
    let background = client.clone();
    tokio::spawn(async move {
        let _ = background
            .track(Event::new("Background job").url("https://example.com/jobs"))
            .await;
    })
    .await?;

    println!("sent events to {}", client.endpoint());
    Ok(())
}
