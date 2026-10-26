# supermetry/laravel

Server-side event tracking for [Supermetry](../../README.md) (self-hosted web analytics),
with a framework-free PHP client and Laravel 10 / 11 / 12 integration.

- `Supermetry\Client`: plain PHP + Guzzle, usable without Laravel.
- Laravel: auto-discovered service provider, `Supermetry` facade, publishable config,
  `@supermetry` Blade directive, optional queued sending and a pageview middleware.

Requires PHP 8.1+. License: Apache-2.0.

## Install

```bash
composer require supermetry/laravel
```

```dotenv
SUPERMETRY_SITE=your-public-site-id
SUPERMETRY_HOST=https://analytics.supermetry.com
# optional
SUPERMETRY_ENABLED=true
SUPERMETRY_QUEUE=false
```

Publish the config (optional):

```bash
php artisan vendor:publish --tag=supermetry-config
```

## Quick start (Laravel)

```php
use Supermetry\Laravel\Facades\Supermetry;

// URL, referrer, User-Agent, IP and DNT/Sec-GPC come from the current request.
Supermetry::track('Signup', ['plan' => 'pro']);

Supermetry::track('Purchase', ['sku' => 'A-1'], [
    'revenue' => ['amount' => '49.90', 'currency' => 'USD'],
]);

Supermetry::pageview();                               // current request URL
Supermetry::pageview('https://supermetry.com/pricing');  // explicit URL

Supermetry::identify((string) $user->id, ['plan' => 'pro']); // later events carry profile_id
Supermetry::reset();
```

Every call accepts an `$options` array that overrides the request defaults: `url`,
`referrer`, `user_agent`, `ip`, `profile_id`, `revenue`, `screen_width`, `dnt`, `gpc`,
`headers`.

Requests are sent asynchronously and settled after the response has been sent (in
Laravel's `terminating` phase). Tracking never throws unless `supermetry.throw` is true;
failures are logged with `Log::warning()` (disable with `log_errors => false`).

### Browser script

```blade
<head>
    @supermetry
    {{-- or: @supermetry(['full' => true, 'hash' => true, 'nonce' => $cspNonce]) --}}
</head>
```

renders `<script defer data-site="…" src="{host}/js/script.js"></script>` (`script.full.js`
with `full`). Extra keys become `data-*` attributes. Nothing is rendered when tracking is
disabled or unconfigured.

### Queued sending

Set `SUPERMETRY_QUEUE=true` (and optionally `SUPERMETRY_QUEUE_CONNECTION` /
`SUPERMETRY_QUEUE_NAME`). The event is built during the request (so `id`, `ts`, URL, UA and IP
are captured then) and delivered by `Supermetry\Laravel\Jobs\SendEvent`. Network errors,
429 and 5xx are retried up to 3 times with the same `id`/`ts` (the server deduplicates);
400s are dropped.

### Server-side pageviews

```php
// routes/web.php
Route::middleware('supermetry.pageview')->group(function () {
    Route::get('/', HomeController::class);
});
```

`Supermetry\Laravel\Http\Middleware\TrackPageview` tracks successful (2xx) HTML `GET`
responses and skips JSON/XHR/prefetch requests and paths in `supermetry.middleware.except`.
Use it **instead of** the browser script, not with it, or pageviews are counted twice.

## Plain PHP

```php
$supermetry = new Supermetry\Client([
    'site' => 'your-public-site-id',
    'host' => 'https://analytics.supermetry.com',
    'url'  => 'https://supermetry.com/',          // default page URL (optional)
    'on_error' => fn (Supermetry\SupermetryException $e) => error_log($e->getMessage()),
]);

$supermetry->track('Signup', ['plan' => 'pro'], [
    'url' => 'https://supermetry.com/signup',
    'user_agent' => $_SERVER['HTTP_USER_AGENT'] ?? null,
    'ip' => $_SERVER['REMOTE_ADDR'] ?? null,
]);
$supermetry->flush(); // also runs automatically when the client is destroyed
```

Config keys: `site`, `host`, `endpoint`, `enabled`, `url`, `user_agent`, `timeout`,
`async` (default true), `send_origin` (default true), `throw` (default false), `on_error`.
`buildEvent()` + `sendNow()` give you a synchronous, retryable path (`sendNow()` returns
`bool`; `lastError()->retryable` tells you whether to resend the same payload).

## Things to know

- **Forward the visitor's User-Agent and IP.** The server derives visitors from IP + UA and
  drops generic HTTP-client UAs as bots. Without a forwarded UA, events are sent as
  `supermetry-php/0.1.0` and all look like one visitor. The IP goes in `X-Forwarded-For`,
  which Supermetry only trusts from addresses in `SUPERMETRY_TRUSTED_PROXIES`: add your
  app servers there.
- **Strict origin**: sites with strict origin checking require an `Origin`/`Referer`. The
  client sends `Origin` derived from the event URL, but turning strict origin off for sites
  that receive server-side events is recommended.
- The event URL's host must be one of the site's allowed hosts, or the server answers 400.
- Local validation mirrors the server: props are capped at 30 keys (string/number/bool
  values), traits are only sent on `identify` and invalid traits are dropped, revenue is
  ignored on pageviews, bodies over 8 KB are rejected before sending.

## Development

```bash
docker run --rm -v "$PWD":/app -w /app composer:2 composer update
docker run --rm -v "$PWD":/app -w /app php:8.3-cli vendor/bin/phpunit
```
