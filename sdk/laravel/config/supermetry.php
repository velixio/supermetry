<?php

return [
    // Public site ID from the Supermetry dashboard.
    'site' => env('SUPERMETRY_SITE'),

    // Base URL of your Supermetry instance, e.g. https://analytics.example.com
    'host' => env('SUPERMETRY_HOST'),

    // Full events endpoint; defaults to {host}/api/public/v1/events.
    'endpoint' => env('SUPERMETRY_ENDPOINT'),

    // Turn all tracking (and the @supermetry script tag) off.
    'enabled' => env('SUPERMETRY_ENABLED', true),

    // Send events through a queued job instead of from the request (true/false).
    'queue' => env('SUPERMETRY_QUEUE', false),
    'queue_connection' => env('SUPERMETRY_QUEUE_CONNECTION'),
    'queue_name' => env('SUPERMETRY_QUEUE_NAME'),

    // Default page URL for events tracked outside an HTTP request (console, jobs).
    'url' => env('SUPERMETRY_URL'),

    // Request timeout in seconds.
    'timeout' => 2.0,

    // Send an Origin header derived from the event URL.
    'send_origin' => true,

    // Forward the visitor's DNT / Sec-GPC headers so sites that respect them drop the event.
    'forward_privacy_signals' => true,

    // Throw Supermetry\SupermetryException from tracking calls instead of only logging.
    'throw' => false,

    // Log delivery / validation errors with Log::warning().
    'log_errors' => true,

    // Defaults for the @supermetry Blade directive, e.g. ['full' => true, 'hash' => true].
    'script' => [],

    // TrackPageview middleware: request paths (Request::is patterns) to skip.
    'middleware' => [
        'except' => [],
    ],
];
