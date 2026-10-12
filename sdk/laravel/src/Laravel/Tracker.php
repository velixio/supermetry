<?php

declare(strict_types=1);

namespace Supermetry\Laravel;

use Closure;
use Illuminate\Contracts\Bus\Dispatcher;
use Illuminate\Http\Request;
use Supermetry\Client;
use Supermetry\Laravel\Jobs\SendEvent;

/**
 * Request-aware wrapper around {@see Client}, resolved by the `Supermetry` facade.
 *
 * Events default their URL, referrer, User-Agent, IP and privacy signals (DNT / Sec-GPC)
 * from the current HTTP request. Anything passed in `$options` wins.
 */
class Tracker
{
    /**
     * @param Closure(): ?Request $requestResolver
     * @param array<string, mixed> $config the `supermetry` config array
     */
    public function __construct(
        private readonly Client $client,
        private readonly Closure $requestResolver,
        private readonly ?Dispatcher $bus,
        private readonly array $config = [],
    ) {
    }

    public function client(): Client
    {
        return $this->client;
    }

    public function enabled(): bool
    {
        return $this->client->isEnabled();
    }

    /**
     * @param array<string, scalar> $props
     * @param array<string, mixed>  $options revenue, profile_id, url, referrer, screen_width,
     *                                       user_agent, ip, dnt, gpc, headers
     */
    public function track(string $name, array $props = [], array $options = []): void
    {
        $this->dispatch($name, $props, $options);
    }

    /**
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
     * @param array<string, scalar> $traits
     * @param array<string, mixed>  $options
     */
    public function identify(?string $profileId, array $traits = [], array $options = []): void
    {
        if ($profileId === null || $profileId === '') {
            $this->client->reset();
            return;
        }
        if (!Client::validProfileId($profileId)) {
            // Let the client report the invalid ID through its error path.
            $this->client->identify($profileId);
            return;
        }
        // Store the profile on the client so later events in this request carry it.
        $options['profile_id'] = $profileId;
        $options['traits'] = $traits;
        $this->dispatch('identify', [], $options, $profileId);
    }

    public function reset(): void
    {
        $this->client->reset();
    }

    public function flush(): void
    {
        $this->client->flush();
    }

    /**
     * The browser tracker `<script>` tag, as rendered by the `@supermetry` Blade directive.
     *
     * Options: `full` (bool, load script.full.js), `src` (override URL), `api` (override
     * data-api), `defer` (bool, default true), `nonce`, plus any other key, rendered as a
     * `data-{key}` attribute (`true` renders a bare attribute, `false`/null is omitted).
     *
     * @param array<string, mixed> $options
     */
    public function script(array $options = []): string
    {
        $site = (string) ($this->config['site'] ?? '');
        $host = rtrim((string) ($this->config['host'] ?? ''), '/');
        if (!self::truthy($this->config['enabled'] ?? true) || $site === '' || $host === '') {
            return '';
        }
        $configured = is_array($this->config['script'] ?? null) ? $this->config['script'] : [];
        $options = array_merge($configured, $options);

        $full = self::truthy($options['full'] ?? false);
        $src = $options['src'] ?? $host . '/js/' . ($full ? 'script.full.js' : 'script.js');
        $attributes = [];
        if (self::truthy($options['defer'] ?? true)) {
            $attributes[] = 'defer';
        }
        $attributes[] = 'data-site="' . e($site) . '"';
        if (isset($options['api']) && is_string($options['api']) && $options['api'] !== '') {
            $attributes[] = 'data-api="' . e($options['api']) . '"';
        }
        if (isset($options['nonce']) && is_string($options['nonce']) && $options['nonce'] !== '') {
            $attributes[] = 'nonce="' . e($options['nonce']) . '"';
        }
        foreach ($options as $key => $value) {
            if (!is_string($key) || in_array($key, ['full', 'src', 'api', 'defer', 'nonce', 'site'], true)) {
                continue;
            }
            if ($value === null || $value === false) {
                continue;
            }
            $name = 'data-' . preg_replace('/[^a-z0-9-]/', '-', strtolower($key));
            $attributes[] = $value === true ? $name : $name . '="' . e((string) $value) . '"';
        }
        $attributes[] = 'src="' . e((string) $src) . '"';

        return '<script ' . implode(' ', $attributes) . '></script>';
    }

    /**
     * Request-derived defaults for an event.
     *
     * @return array<string, mixed>
     */
    public function requestContext(): array
    {
        $request = ($this->requestResolver)();
        if (!$request instanceof Request) {
            return [];
        }
        $context = [];
        $context['url'] = $request->fullUrl();
        $referrer = $request->headers->get('referer');
        if (is_string($referrer) && $referrer !== '') {
            $context['referrer'] = $referrer;
        }
        $userAgent = $request->userAgent();
        if (is_string($userAgent) && $userAgent !== '') {
            $context['user_agent'] = $userAgent;
        }
        $ip = $request->ip();
        if (is_string($ip) && $ip !== '') {
            $context['ip'] = $ip;
        }
        if (self::truthy($this->config['forward_privacy_signals'] ?? true)) {
            if ($request->headers->get('DNT') === '1') {
                $context['dnt'] = true;
            }
            if ($request->headers->get('Sec-GPC') === '1') {
                $context['gpc'] = true;
            }
        }
        return $context;
    }

    /**
     * @param array<string, mixed> $props
     * @param array<string, mixed> $options
     */
    private function dispatch(string $name, array $props, array $options, ?string $identify = null): void
    {
        if (!$this->client->isEnabled()) {
            return;
        }
        $options = array_merge($this->requestContext(), $options);
        $payload = $this->client->buildEvent($name, $props, $options);
        if ($identify !== null) {
            $this->client->setProfileId($identify);
        }
        if ($payload === null) {
            return;
        }
        $context = array_intersect_key($options, array_flip(['user_agent', 'ip', 'dnt', 'gpc', 'headers']));

        $queue = $this->config['queue'] ?? false;
        if ($this->bus !== null && self::truthy($queue)) {
            $job = new SendEvent($payload, $context);
            $connection = $this->config['queue_connection'] ?? null;
            if (is_string($connection) && $connection !== '') {
                $job->onConnection($connection);
            }
            $queueName = $this->config['queue_name'] ?? null;
            if (is_string($queueName) && $queueName !== '') {
                $job->onQueue($queueName);
            }
            $this->bus->dispatch($job);
            return;
        }
        $this->client->send($payload, $context);
    }

    private static function truthy(mixed $value): bool
    {
        return is_string($value) ? filter_var($value, FILTER_VALIDATE_BOOLEAN) : (bool) $value;
    }
}
