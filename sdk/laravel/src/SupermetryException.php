<?php

declare(strict_types=1);

namespace Supermetry;

use RuntimeException;
use Throwable;

/**
 * Raised (or handed to the `on_error` hook) when an event cannot be built or delivered.
 *
 * `status` is the HTTP status for a rejected request, or null for local validation and
 * network failures. `payload` is the event that failed, when one was built. `retryable` is
 * true for network errors, 429 and 5xx (resend the same payload; its `id` deduplicates).
 */
final class SupermetryException extends RuntimeException
{
    /**
     * @param array<string, mixed>|null $payload
     */
    public function __construct(
        string $message,
        public readonly ?int $status = null,
        public readonly ?array $payload = null,
        ?Throwable $previous = null,
        public readonly bool $retryable = false,
    ) {
        parent::__construct($message, $status ?? 0, $previous);
    }
}
