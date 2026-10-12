<?php

declare(strict_types=1);

namespace Supermetry\Laravel\Jobs;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Bus\Dispatchable;
use Illuminate\Queue\InteractsWithQueue;
use Illuminate\Queue\SerializesModels;
use Supermetry\Client;

/**
 * Queued delivery of one pre-built event. The payload keeps its `id` and `ts`, so a
 * retried job is deduplicated server-side and keeps the original event time.
 */
class SendEvent implements ShouldQueue
{
    use Dispatchable;
    use InteractsWithQueue;
    use Queueable;
    use SerializesModels;

    public int $tries = 3;

    /** @var list<int> */
    public array $backoff = [5, 30];

    /**
     * @param array<string, mixed> $payload
     * @param array<string, mixed> $context user_agent, ip, dnt, gpc, headers
     */
    public function __construct(
        public readonly array $payload,
        public readonly array $context = [],
    ) {
    }

    public function handle(Client $client): void
    {
        if ($client->sendNow($this->payload, $this->context)) {
            return;
        }
        $error = $client->lastError();
        // Retry network failures, 429 and 5xx with the same payload; drop 400s and invalid events.
        if ($error !== null && $error->retryable && $this->attempts() < $this->tries) {
            $this->release($this->backoff[$this->attempts() - 1] ?? 30);
        }
    }
}
