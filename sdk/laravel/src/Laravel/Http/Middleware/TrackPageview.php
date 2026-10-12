<?php

declare(strict_types=1);

namespace Supermetry\Laravel\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use Supermetry\Laravel\Tracker;
use Symfony\Component\HttpFoundation\Response;

/**
 * Sends a server-side `pageview` for successful HTML GET responses.
 *
 * Skips non-GET/HEAD requests, JSON/XHR and prefetch requests, non-2xx responses, non-HTML
 * responses and paths matching `supermetry.middleware.except`. Use it instead of (not in
 * addition to) the browser script, or you will count every page twice.
 */
class TrackPageview
{
    public function __construct(private readonly Tracker $tracker)
    {
    }

    public function handle(Request $request, Closure $next): mixed
    {
        $response = $next($request);

        try {
            if ($response instanceof Response && $this->shouldTrack($request, $response)) {
                $this->tracker->pageview();
            }
        } catch (\Throwable $e) {
            report($e);
        }

        return $response;
    }

    protected function shouldTrack(Request $request, Response $response): bool
    {
        if (!$this->tracker->enabled() || !$request->isMethod('GET')) {
            return false;
        }
        if ($request->ajax() || $request->expectsJson() || $request->pjax() || $request->prefetch()) {
            return false;
        }
        $status = $response->getStatusCode();
        if ($status < 200 || $status >= 300) {
            return false;
        }
        $contentType = (string) $response->headers->get('Content-Type', 'text/html');
        if (!str_contains(strtolower($contentType), 'text/html')) {
            return false;
        }
        $except = config('supermetry.middleware.except', []);
        if (is_array($except) && $except !== [] && $request->is(...$except)) {
            return false;
        }
        return true;
    }
}
