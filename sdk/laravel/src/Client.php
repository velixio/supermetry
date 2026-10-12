<?php

declare(strict_types=1);

namespace Supermetry;

use GuzzleHttp\Client as GuzzleClient;
use GuzzleHttp\ClientInterface;
use GuzzleHttp\Promise\PromiseInterface;
use GuzzleHttp\Promise\Utils;
use Psr\Http\Message\ResponseInterface;
use Throwable;

/**
 * Framework-free Supermetry event client.
 *
 * Events are validated locally, then sent to `POST {host}/api/public/v1/events`.
 * By default requests are sent asynchronously and settled on {@see flush()} (called
 * automatically on destruction); tracking never throws into the host application
 * unless `throw` is enabled.
 *
 * Config keys:
 *  - `site` (string, required): public site ID.
 *  - `host` (string): Supermetry base URL, e.g. `https://analytics.example.com`.
 *  - `endpoint` (string): full events endpoint, overrides `host`.
 *  - `enabled` (bool, default true): when false every call is a no-op.
 *  - `url` (string): default page URL for events that do not pass one.
 *  - `user_agent` (string): UA sent when the caller does not forward a visitor UA.
 *  - `timeout` (float, default 2.0): request timeout in seconds.
 *  - `async` (bool, default true): queue requests until flush() instead of blocking.
 *  - `send_origin` (bool, default true): send an `Origin` header derived from the event URL.
 *  - `throw` (bool, default false): throw {@see SupermetryException} instead of only reporting.
 *  - `on_error` (callable(SupermetryException): void): error hook.
 */
final class Client
{
    public const VERSION = '0.1.0';
    public const USER_AGENT = 'supermetry-php/' . self::VERSION;
    public const EVENTS_PATH = '/api/public/v1/events';

    public const MAX_BODY_BYTES = 8192;
    public const MAX_NAME_BYTES = 300;
    public const MAX_URL_BYTES = 2000;
    public const MAX_PROPS = 30;
    public const MAX_PROP_KEY_BYTES = 300;
    public const MAX_PROP_VALUE_BYTES = 2000;
    public const MAX_PROFILE_ID_CHARS = 128;
    public const MAX_TRAITS = 20;
    public const MAX_TRAIT_KEY_CHARS = 64;
    public const MAX_TRAIT_VALUE_CHARS = 256;

    private readonly ClientInterface $http;
    private readonly string $site;
    private readonly ?string $endpoint;
    private readonly bool $enabled;
    private readonly ?string $defaultUrl;
    private readonly string $userAgent;
    private readonly bool $async;
    private readonly bool $sendOrigin;
    private readonly bool $throw;
    /** @var (callable(SupermetryException): void)|null */
    private $onError;

    private ?string $profileId = null;

    private ?SupermetryException $lastError = null;

    /** @var list<PromiseInterface> */
    private array $pending = [];

    /**
     * Errors from async responses, collected outside $this so pending promises do not form
     * a reference cycle with the client (which would delay the flushing destructor).
     *
     * @var \ArrayObject<int, SupermetryException>
     */
    private \ArrayObject $deferredErrors;

    /**
     * @param array<string, mixed> $config
     */
    public function __construct(array $config, ?ClientInterface $http = null)
    {
        $this->site = (string) ($config['site'] ?? '');
        $endpoint = $config['endpoint'] ?? null;
        $host = $config['host'] ?? null;
        if (is_string($endpoint) && $endpoint !== '') {
            $this->endpoint = $endpoint;
        } elseif (is_string($host) && $host !== '') {
            $this->endpoint = rtrim($host, '/') . self::EVENTS_PATH;
        } else {
            $this->endpoint = null;
        }
        $this->enabled = self::bool($config['enabled'] ?? true) && $this->site !== '' && $this->endpoint !== null;
        $url = $config['url'] ?? null;
        $this->defaultUrl = is_string($url) && $url !== '' ? $url : null;
        $userAgent = $config['user_agent'] ?? null;
        $this->userAgent = is_string($userAgent) && $userAgent !== '' ? $userAgent : self::USER_AGENT;
        $this->async = self::bool($config['async'] ?? true);
        $this->sendOrigin = self::bool($config['send_origin'] ?? true);
        $this->throw = self::bool($config['throw'] ?? false);
        $this->onError = isset($config['on_error']) && is_callable($config['on_error']) ? $config['on_error'] : null;
        $this->deferredErrors = new \ArrayObject();
        $timeout = (float) ($config['timeout'] ?? 2.0);
        $this->http = $http ?? new GuzzleClient([
            'timeout' => $timeout,
            'connect_timeout' => $timeout,
            'http_errors' => false,
        ]);
    }

    public function __destruct()
    {
        try {
            $this->flush();
        } catch (Throwable) {
            // Never throw from a destructor.
        }
    }

    public function isEnabled(): bool
    {
        return $this->enabled;
    }

    public function endpoint(): ?string
    {
        return $this->endpoint;
    }

    public function site(): string
    {
        return $this->site;
    }

    public function profileId(): ?string
    {
        return $this->profileId;
    }

    /**
     * Track a custom event.
     *
     * @param array<string, scalar> $props
     * @param array<string, mixed>  $options revenue, profile_id, url, referrer, screen_width,
     *                                       user_agent, ip, dnt, gpc, headers, id, ts
     */
    public function track(string $name, array $props = [], array $options = []): void
    {
        $this->dispatch($name, $props, $options);
    }

    /**
     * Track a pageview. `$url` falls back to `$options['url']`, then the configured default URL.
     *
     * @param array<string, scalar> $props
     * @param array<string, mixed>  $options
     */
    public function pageview(?string $url = null, array $props = [], array $options = []): void
    {
        if ($url !== null) {
            $options['url'] = $url;
        }
        $this->dispatch('pageview', $props, $options);
    }

    /**
     * Identify the current user. Stores the profile ID so later events carry `profile_id`.
     * Passing null clears it (same as {@see reset()}) and sends nothing.
     *
     * @param array<string, scalar> $traits
     * @param array<string, mixed>  $options
     */
    public function identify(?string $profileId, array $traits = [], array $options = []): void
    {
        if ($profileId === null || $profileId === '') {
            $this->reset();
            return;
        }
        if (!self::validProfileId($profileId)) {
            $this->report(new SupermetryException('invalid profile_id'));
            return;
        }
        $this->profileId = $profileId;
        $options['profile_id'] = $profileId;
        $options['traits'] = $traits;
        $this->dispatch('identify', [], $options);
    }

    /**
     * Set (or clear with null) the stored profile ID without sending an identify event.
     */
    public function setProfileId(?string $profileId): void
    {
        $this->profileId = $profileId !== null && self::validProfileId($profileId) ? $profileId : null;
    }

    /** Forget the identified profile ID. */
    public function reset(): void
    {
        $this->profileId = null;
    }

    /**
     * Build and validate an event payload without sending it.
     * Returns null (after reporting) when the event is invalid or the client is disabled.
     *
     * @param array<string, mixed> $props
     * @param array<string, mixed> $options
     * @return array<string, mixed>|null
     */
    public function buildEvent(string $name, array $props = [], array $options = []): ?array
    {
        if (!$this->enabled) {
            return null;
        }
        if ($name === '' || strlen($name) > self::MAX_NAME_BYTES || !self::utf8($name)) {
            $this->report(new SupermetryException('invalid event name'));
            return null;
        }
        $url = $options['url'] ?? $this->defaultUrl;
        if (!is_string($url) || !self::validUrl($url)) {
            $this->report(new SupermetryException('event url is missing or invalid'));
            return null;
        }

        $id = $options['id'] ?? null;
        $payload = [
            'v' => 1,
            'id' => is_string($id) && preg_match('/^[A-Za-z0-9_-]{1,64}$/', $id) === 1 ? $id : self::newId(),
            'ts' => is_int($options['ts'] ?? null) ? $options['ts'] : (int) floor(microtime(true) * 1000),
            'site' => $this->site,
            'name' => $name,
            'url' => $url,
        ];

        $referrer = $options['referrer'] ?? null;
        if (is_string($referrer) && $referrer !== '' && strlen($referrer) <= self::MAX_URL_BYTES && self::utf8($referrer)) {
            $payload['referrer'] = $referrer;
        }
        $screenWidth = $options['screen_width'] ?? null;
        if (is_int($screenWidth) && $screenWidth >= 0 && $screenWidth <= 32767) {
            $payload['screen_width'] = $screenWidth;
        }
        $cleanProps = self::cleanProps($props);
        if ($cleanProps !== []) {
            $payload['props'] = $cleanProps;
        }
        if ($name !== 'pageview' && isset($options['revenue'])) {
            $revenue = self::cleanRevenue($options['revenue']);
            if ($revenue !== null) {
                $payload['revenue'] = $revenue;
            }
        }
        $profileId = array_key_exists('profile_id', $options) ? $options['profile_id'] : $this->profileId;
        if (is_string($profileId) && self::validProfileId($profileId)) {
            $payload['profile_id'] = $profileId;
        }
        if ($name === 'identify') {
            if (!isset($payload['profile_id'])) {
                $this->report(new SupermetryException('identify requires a profile_id'));
                return null;
            }
            $traits = self::cleanTraits(is_array($options['traits'] ?? null) ? $options['traits'] : []);
            if ($traits !== []) {
                $payload['traits'] = $traits;
            }
        }
        return $payload;
    }

    /**
     * Request headers for an event. `$context` keys: user_agent, ip, dnt, gpc, headers.
     *
     * @param array<string, mixed> $payload
     * @param array<string, mixed> $context
     * @return array<string, string>
     */
    public function headersFor(array $payload, array $context = []): array
    {
        $userAgent = $context['user_agent'] ?? null;
        $headers = [
            'Content-Type' => 'application/json',
            'User-Agent' => is_string($userAgent) && $userAgent !== '' ? $userAgent : $this->userAgent,
        ];
        $ip = $context['ip'] ?? null;
        if (is_string($ip) && $ip !== '') {
            $headers['X-Forwarded-For'] = $ip;
        }
        if ($this->sendOrigin && isset($payload['url']) && is_string($payload['url'])) {
            $origin = self::originOf($payload['url']);
            if ($origin !== null) {
                $headers['Origin'] = $origin;
            }
        }
        if (self::bool($context['dnt'] ?? false)) {
            $headers['DNT'] = '1';
        }
        if (self::bool($context['gpc'] ?? false)) {
            $headers['Sec-GPC'] = '1';
        }
        foreach (is_array($context['headers'] ?? null) ? $context['headers'] : [] as $key => $value) {
            if (is_string($key) && is_scalar($value)) {
                $headers[$key] = (string) $value;
            }
        }
        return $headers;
    }

    /**
     * Send an already built payload. Asynchronous unless the client was created with
     * `async => false`; call {@see flush()} to wait for pending requests.
     *
     * @param array<string, mixed> $payload
     * @param array<string, mixed> $context
     */
    public function send(array $payload, array $context = []): void
    {
        if (!$this->enabled) {
            return;
        }
        if (!$this->async) {
            $this->sendNow($payload, $context);
            return;
        }
        $request = $this->encode($payload, $context);
        if ($request === null) {
            return;
        }
        [$body, $headers] = $request;
        $errors = $this->deferredErrors;
        try {
            $this->pending[] = $this->http->requestAsync('POST', (string) $this->endpoint, [
                'headers' => $headers,
                'body' => $body,
                'http_errors' => false,
            ])->then(
                static function (ResponseInterface $response) use ($payload, $errors): void {
                    $error = self::responseError($response, $payload);
                    if ($error !== null) {
                        $errors[] = $error;
                    }
                },
                static function (mixed $reason) use ($payload, $errors): void {
                    $errors[] = self::networkError($reason, $payload);
                },
            );
        } catch (Throwable $e) {
            $this->report(self::networkError($e, $payload));
        }
    }

    /**
     * Send a payload synchronously. Returns true when the server accepted it (2xx).
     *
     * @param array<string, mixed> $payload
     * @param array<string, mixed> $context
     */
    public function sendNow(array $payload, array $context = []): bool
    {
        if (!$this->enabled) {
            return false;
        }
        $this->lastError = null;
        $request = $this->encode($payload, $context);
        if ($request === null) {
            return false;
        }
        [$body, $headers] = $request;
        try {
            $response = $this->http->request('POST', (string) $this->endpoint, [
                'headers' => $headers,
                'body' => $body,
                'http_errors' => false,
            ]);
        } catch (Throwable $e) {
            $this->report(self::networkError($e, $payload));
            return false;
        }
        $error = self::responseError($response, $payload);
        if ($error !== null) {
            $this->report($error);
            return false;
        }
        return true;
    }

    /** Wait for every pending asynchronous request and report failures. */
    public function flush(): void
    {
        while ($this->pending !== []) {
            $pending = $this->pending;
            $this->pending = [];
            try {
                Utils::settle($pending)->wait();
            } catch (Throwable) {
                // Rejections are captured by the per-request handlers.
            }
        }
        $errors = $this->deferredErrors->getArrayCopy();
        $this->deferredErrors->exchangeArray([]);
        foreach ($errors as $error) {
            $this->report($error);
        }
    }

    /** Number of requests queued and not yet flushed. */
    public function pendingCount(): int
    {
        return count($this->pending);
    }

    /**
     * @param array<string, mixed> $props
     * @param array<string, mixed> $options
     */
    private function dispatch(string $name, array $props, array $options): void
    {
        $payload = $this->buildEvent($name, $props, $options);
        if ($payload !== null) {
            $this->send($payload, $options);
        }
    }

    /**
     * @param array<string, mixed> $payload
     * @param array<string, mixed> $context
     * @return array{0: string, 1: array<string, string>}|null
     */
    private function encode(array $payload, array $context): ?array
    {
        $body = json_encode($payload, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PRESERVE_ZERO_FRACTION);
        if ($body === false) {
            $this->report(new SupermetryException('event could not be encoded', null, $payload));
            return null;
        }
        if (strlen($body) > self::MAX_BODY_BYTES) {
            $this->report(new SupermetryException('event body exceeds 8 KB', null, $payload));
            return null;
        }
        return [$body, $this->headersFor($payload, $context)];
    }

    /** The most recent error reported by this client, if any. */
    public function lastError(): ?SupermetryException
    {
        return $this->lastError;
    }

    private function report(SupermetryException $error): void
    {
        $this->lastError = $error;
        if ($this->onError !== null) {
            try {
                ($this->onError)($error);
            } catch (Throwable) {
                // An error hook must not break tracking.
            }
        }
        if ($this->throw) {
            throw $error;
        }
    }

    /**
     * @param array<string, mixed> $payload
     */
    private static function responseError(ResponseInterface $response, array $payload): ?SupermetryException
    {
        $status = $response->getStatusCode();
        if ($status >= 200 && $status < 300) {
            return null;
        }
        $retryable = $status === 429 || $status >= 500;
        return new SupermetryException("supermetry responded with HTTP {$status}", $status, $payload, null, $retryable);
    }

    /**
     * @param array<string, mixed> $payload
     */
    private static function networkError(mixed $reason, array $payload): SupermetryException
    {
        $previous = $reason instanceof Throwable ? $reason : null;
        $message = $previous !== null ? $previous->getMessage() : 'request failed';
        return new SupermetryException('supermetry request failed: ' . $message, null, $payload, $previous, true);
    }

    public static function newId(): string
    {
        return bin2hex(random_bytes(16));
    }

    public static function validUrl(string $url): bool
    {
        if ($url === '' || strlen($url) > self::MAX_URL_BYTES || !self::utf8($url)) {
            return false;
        }
        $parts = parse_url($url);
        if ($parts === false) {
            return false;
        }
        $scheme = strtolower($parts['scheme'] ?? '');
        return ($scheme === 'http' || $scheme === 'https')
            && ($parts['host'] ?? '') !== ''
            && !isset($parts['user'])
            && !isset($parts['pass']);
    }

    public static function validProfileId(string $profileId): bool
    {
        return $profileId !== '' && self::utf8($profileId) && mb_strlen($profileId, 'UTF-8') <= self::MAX_PROFILE_ID_CHARS;
    }

    /**
     * Keep string/number/boolean values with valid keys, truncate long strings, cap at 30 keys.
     *
     * @param array<array-key, mixed> $props
     * @return array<string, string|int|float|bool>
     */
    public static function cleanProps(array $props): array
    {
        $clean = [];
        foreach ($props as $key => $value) {
            if (count($clean) >= self::MAX_PROPS) {
                break;
            }
            $key = (string) $key;
            if ($key === '' || strlen($key) > self::MAX_PROP_KEY_BYTES || !self::utf8($key)) {
                continue;
            }
            if (is_string($value)) {
                if (!self::utf8($value)) {
                    continue;
                }
                if (strlen($value) > self::MAX_PROP_VALUE_BYTES) {
                    $value = mb_strcut($value, 0, self::MAX_PROP_VALUE_BYTES, 'UTF-8');
                }
                $clean[$key] = $value;
            } elseif (is_int($value) || is_bool($value)) {
                $clean[$key] = $value;
            } elseif (is_float($value) && is_finite($value)) {
                $clean[$key] = $value;
            } elseif ($value instanceof \Stringable) {
                $clean[$key] = (string) $value;
            }
        }
        return $clean;
    }

    /**
     * Keep string-able traits with valid key/value lengths, at most 20.
     *
     * @param array<array-key, mixed> $traits
     * @return array<string, string>
     */
    public static function cleanTraits(array $traits): array
    {
        $clean = [];
        foreach ($traits as $key => $value) {
            if (count($clean) >= self::MAX_TRAITS) {
                break;
            }
            $key = (string) $key;
            if (is_bool($value)) {
                $value = $value ? 'true' : 'false';
            } elseif (is_int($value) || is_float($value) || $value instanceof \Stringable) {
                $value = (string) $value;
            }
            if (!is_string($value)) {
                continue;
            }
            if ($key === '' || !self::utf8($key) || mb_strlen($key, 'UTF-8') > self::MAX_TRAIT_KEY_CHARS) {
                continue;
            }
            if (!self::utf8($value) || mb_strlen($value, 'UTF-8') > self::MAX_TRAIT_VALUE_CHARS) {
                continue;
            }
            $clean[$key] = $value;
        }
        return $clean;
    }

    /**
     * Normalise a revenue value: `['amount' => '49.90' | 49.9, 'currency' => 'USD']`,
     * or a bare amount. Returns null when invalid.
     *
     * @return array{amount: string|int, currency?: string}|null
     */
    public static function cleanRevenue(mixed $revenue): ?array
    {
        if (!is_array($revenue)) {
            $revenue = ['amount' => $revenue];
        }
        $amount = $revenue['amount'] ?? null;
        if (is_int($amount)) {
            if ($amount <= 0) {
                return null;
            }
        } elseif (is_float($amount)) {
            if (!is_finite($amount) || $amount <= 0) {
                return null;
            }
            $amount = rtrim(rtrim(number_format($amount, 6, '.', ''), '0'), '.');
            if ($amount === '' || $amount === '0') {
                return null;
            }
        } elseif (is_string($amount)) {
            if (preg_match('/^\d+(\.\d{1,6})?$/', $amount) !== 1 || (float) $amount <= 0) {
                return null;
            }
        } else {
            return null;
        }
        $clean = ['amount' => $amount];
        $currency = $revenue['currency'] ?? null;
        if ($currency !== null && $currency !== '') {
            if (!is_string($currency)) {
                return null;
            }
            $currency = strtoupper($currency);
            if (preg_match('/^[A-Z]{3}$/', $currency) !== 1) {
                return null;
            }
            $clean['currency'] = $currency;
        }
        return $clean;
    }

    public static function originOf(string $url): ?string
    {
        $parts = parse_url($url);
        if ($parts === false || !isset($parts['scheme'], $parts['host'])) {
            return null;
        }
        $origin = strtolower($parts['scheme']) . '://' . $parts['host'];
        if (isset($parts['port'])) {
            $origin .= ':' . $parts['port'];
        }
        return $origin;
    }

    private static function utf8(string $value): bool
    {
        return mb_check_encoding($value, 'UTF-8');
    }

    private static function bool(mixed $value): bool
    {
        if (is_string($value)) {
            return filter_var($value, FILTER_VALIDATE_BOOLEAN);
        }
        return (bool) $value;
    }
}
