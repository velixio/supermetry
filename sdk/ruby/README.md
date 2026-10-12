# supermetry (Ruby)

Server-side SDK for [Supermetry](https://supermetry.com), self-hosted web analytics.
Ruby >= 3.0, no runtime dependencies (`net/http`, `json`, `securerandom`).

## Install

```ruby
# Gemfile
gem "supermetry", "~> 0.1"
```

or `gem install supermetry`.

## Quick start

```ruby
require "supermetry"

client = Supermetry::Client.new(
  site: "your-site-id",
  host: "https://analytics.example.com",
  url: "https://example.com" # default page URL for events without one
)

# In a Rails/Rack handler, forward the visitor's UA and IP:
client.pageview(url: request.url, user_agent: request.user_agent, ip: request.remote_ip,
                referrer: request.referrer)

client.track("Purchase",
             url: "https://example.com/checkout",
             props: { plan: "pro", seats: 5, annual: true },
             revenue: { amount: "490.00", currency: "USD" },
             profile_id: current_user.id,
             user_agent: request.user_agent,
             ip: request.remote_ip)

client.identify(current_user.id, { plan: "pro", company: "Acme" },
                user_agent: request.user_agent, ip: request.remote_ip)

client.flush    # wait for queued events
client.shutdown # flush and stop the background thread (also runs at exit)
```

### Module-level default client

```ruby
# config/initializers/supermetry.rb
Supermetry.configure(site: "your-site-id", host: "https://analytics.example.com") do |c|
  c.url = "https://example.com"
  c.logger = Rails.logger
end

Supermetry.track("Signup", url: request.url, user_agent: request.user_agent, ip: request.remote_ip)
Supermetry.pageview(url: request.url, user_agent: request.user_agent, ip: request.remote_ip)
Supermetry.identify(user.id, { plan: "pro" }, user_agent: request.user_agent, ip: request.remote_ip)
```

Calls before `configure` are no-ops returning `false`.

## Forward the visitor's User-Agent and IP

Supermetry derives visitors from IP + User-Agent, so pass the visitor's real values with
every event (`user_agent:` and `ip:`). The IP is sent as `X-Forwarded-For`, which the
server only trusts from addresses listed in `SUPERMETRY_TRUSTED_PROXIES`, so add your
application servers there. Without a forwarded UA the SDK sends `Supermetry-Ruby/0.1.0`;
never send a generic client UA (`curl`, `python-requests`, ...), since those are filtered
as bots.

## Strict origin

Turn *strict origin* **off** for sites that receive server-sent events. The SDK sends an
`Origin` derived from the event URL (disable with `send_origin: false`), but server-side
traffic should not rely on origin checks.

## Options

| option | default | |
|---|---|---|
| `site:` | required | public site ID |
| `host:` | required | Supermetry base URL; endpoint is `host + /api/public/v1/events` |
| `endpoint:` | `nil` | full endpoint override |
| `url:` | `nil` | default page URL |
| `user_agent:` | `Supermetry-Ruby/0.1.0` | fallback UA when none is forwarded |
| `async:` | `true` | send from a background thread; `false` sends inline |
| `logger:` | `nil` | receives warnings for dropped/failed events |
| `on_error:` | `nil` | `->(error, payload) { }` |
| `disabled:` | `false` | turn every call into a no-op |
| `max_queue_size:` | `1000` | events buffered before new ones are dropped |
| `max_retries:` | `1` | retries on network errors, `429` and `5xx` |
| `retry_delay:` | `0.5` | seconds between retries (`Retry-After` honoured up to 5 s) |
| `timeout:` | `5` | open/read/write timeout in seconds |
| `send_origin:` | `true` | send `Origin` derived from the event URL |

## Behaviour

- Tracking methods never raise. They return `true` when the event was queued (async) or
  accepted (sync), `false` otherwise; errors go to `logger` and `on_error`.
- Each event gets `v: 1`, an `id` (UUID without dashes) and `ts` (Unix ms). Retries reuse
  the same `id`, so the server deduplicates. `400` is not retried.
- Local validation: name ≤ 300 bytes; `url` required, absolute `http(s)`, ≤ 2000 bytes,
  no userinfo; `profile_id` 1–128 chars; body ≤ 8 KB. Props must be string, number or
  boolean; invalid ones are dropped and props are capped at 30 keys. Traits are only sent
  by `identify`; invalid traits are dropped and capped at 20. Invalid revenue (more than 6
  decimals, non-positive, lowercase currency) is dropped with a warning while the event is
  still sent. Revenue on `pageview` is ignored.
- The client is shared across visitors and does not remember profile IDs: pass
  `profile_id:` on each event of an identified user.
- After `fork` (Puma/Unicorn workers) the background thread is restarted automatically.

## Development

```sh
gem install --user-install minitest
ruby -w -Ilib -Itest -e 'Dir["test/test_*.rb"].each { |f| require_relative f }'
# or, with rake installed:
rake test
```

License: Apache-2.0
