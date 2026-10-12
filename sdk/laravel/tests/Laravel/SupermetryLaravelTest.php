<?php

declare(strict_types=1);

namespace Supermetry\Tests\Laravel;

use Illuminate\Support\Facades\Blade;
use Illuminate\Support\Facades\Bus;
use Illuminate\Support\Facades\Route;
use Supermetry\Client;
use Supermetry\Laravel\Facades\Supermetry;
use Supermetry\Laravel\Jobs\SendEvent;
use Supermetry\Laravel\Tracker;

final class SupermetryLaravelTest extends TestCase
{
    public function testProviderRegistersBindingsAndConfig(): void
    {
        self::assertInstanceOf(Tracker::class, $this->app->make('supermetry'));
        self::assertInstanceOf(Client::class, $this->app->make(Client::class));
        self::assertSame('https://analytics.example.com/api/public/v1/events', $this->app->make(Client::class)->endpoint());
        self::assertFalse((bool) config('supermetry.queue'));
    }

    public function testFacadeTrackDefaultsFromCurrentRequest(): void
    {
        $this->fakeHttp();
        Route::get('/checkout', function () {
            Supermetry::track('Checkout', ['step' => 2], ['revenue' => ['amount' => '19.99', 'currency' => 'USD']]);
            return 'ok';
        });

        $this->withHeaders([
            'User-Agent' => 'Mozilla/5.0 (Macintosh) Visitor',
            'Referer' => 'https://ref.example.org/',
            'DNT' => '1',
        ])->withServerVariables(['REMOTE_ADDR' => '198.51.100.7'])->get('/checkout?coupon=x')->assertOk();

        self::assertCount(1, $this->history, 'terminating callback flushed the async send');
        $body = $this->sentBody();
        self::assertSame('Checkout', $body['name']);
        self::assertSame('http://localhost/checkout?coupon=x', $body['url']);
        self::assertSame('https://ref.example.org/', $body['referrer']);
        self::assertSame(['step' => 2], $body['props']);
        self::assertSame(['amount' => '19.99', 'currency' => 'USD'], $body['revenue']);
        self::assertSame('Mozilla/5.0 (Macintosh) Visitor', $this->sentHeader(0, 'User-Agent'));
        self::assertSame('198.51.100.7', $this->sentHeader(0, 'X-Forwarded-For'));
        self::assertSame('1', $this->sentHeader(0, 'DNT'));
        self::assertSame('http://localhost', $this->sentHeader(0, 'Origin'));
    }

    public function testIdentifyThenTrackCarriesProfileWithinRequest(): void
    {
        $this->fakeHttp();
        Route::get('/login', function () {
            Supermetry::identify('user-7', ['plan' => 'team']);
            Supermetry::track('Logged in');
            return 'ok';
        });
        $this->get('/login')->assertOk();

        self::assertCount(2, $this->history);
        self::assertSame('identify', $this->sentBody(0)['name']);
        self::assertSame(['plan' => 'team'], $this->sentBody(0)['traits']);
        self::assertSame('user-7', $this->sentBody(1)['profile_id']);
        self::assertArrayNotHasKey('traits', $this->sentBody(1));
    }

    public function testExplicitOptionsOverrideRequest(): void
    {
        $this->fakeHttp();
        Route::get('/x', function () {
            Supermetry::pageview('https://shop.example.com/p/1', [], ['user_agent' => 'Custom/1.0', 'ip' => '192.0.2.1']);
            return 'ok';
        });
        $this->get('/x')->assertOk();
        self::assertSame('https://shop.example.com/p/1', $this->sentBody()['url']);
        self::assertSame('Custom/1.0', $this->sentHeader(0, 'User-Agent'));
        self::assertSame('192.0.2.1', $this->sentHeader(0, 'X-Forwarded-For'));
    }

    public function testQueuedSendingDispatchesJobWithPrebuiltPayload(): void
    {
        config(['supermetry.queue' => true, 'supermetry.queue_name' => 'analytics']);
        $this->app->forgetScopedInstances();
        Bus::fake();

        Route::get('/q', function () {
            Supermetry::track('Queued', ['a' => 'b']);
            return 'ok';
        });
        $this->withHeaders(['User-Agent' => 'Mozilla/5.0 Q'])->get('/q')->assertOk();

        Bus::assertDispatched(SendEvent::class, function (SendEvent $job): bool {
            return $job->payload['name'] === 'Queued'
                && $job->payload['url'] === 'http://localhost/q'
                && preg_match('/^[0-9a-f]{32}$/', $job->payload['id']) === 1
                && is_int($job->payload['ts'])
                && $job->context['user_agent'] === 'Mozilla/5.0 Q'
                && $job->queue === 'analytics';
        });
    }

    public function testJobHandleSendsSynchronouslyKeepingIdAndTs(): void
    {
        $this->fakeHttp();
        $payload = ['v' => 1, 'id' => 'abc', 'ts' => 1_700_000_000_000, 'site' => 'site_123', 'name' => 'Job', 'url' => 'https://app.example.com/'];
        $job = new SendEvent($payload, ['user_agent' => 'Mozilla/5.0 J']);
        $job->handle($this->app->make(Client::class));
        self::assertCount(1, $this->history);
        self::assertSame($payload, $this->sentBody());
        self::assertSame('Mozilla/5.0 J', $this->sentHeader(0, 'User-Agent'));
    }

    public function testBladeDirectiveRendersScriptTag(): void
    {
        self::assertSame(
            '<script defer data-site="site_123" src="https://analytics.example.com/js/script.js"></script>',
            trim(Blade::render('@supermetry')),
        );
        self::assertSame(
            '<script defer data-site="site_123" nonce="n0" data-hash data-consent="required" src="https://analytics.example.com/js/script.full.js"></script>',
            trim(Blade::render("@supermetry(['full' => true, 'nonce' => 'n0', 'hash' => true, 'consent' => 'required', 'manual' => false])")),
        );
    }

    public function testBladeDirectiveEmptyWhenDisabled(): void
    {
        config(['supermetry.enabled' => false]);
        $this->app->forgetScopedInstances();
        self::assertSame('', trim(Blade::render('@supermetry')));
    }

    public function testPageviewMiddlewareTracksHtmlGetsOnly(): void
    {
        $this->fakeHttp();
        config(['supermetry.middleware.except' => ['admin/*']]);
        Route::middleware('supermetry.pageview')->group(function (): void {
            Route::get('/page', fn () => response('<html></html>'));
            Route::get('/api', fn () => response()->json(['ok' => true]));
            Route::get('/missing', fn () => response('nope', 404));
            Route::post('/form', fn () => response('<html></html>'));
            Route::get('/admin/dash', fn () => response('<html></html>'));
        });

        $this->get('/page')->assertOk();
        $this->get('/api')->assertOk();
        $this->get('/missing')->assertNotFound();
        $this->post('/form')->assertOk();
        $this->get('/admin/dash')->assertOk();
        $this->getJson('/page')->assertOk();

        self::assertCount(1, $this->history);
        self::assertSame('pageview', $this->sentBody()['name']);
        self::assertSame('http://localhost/page', $this->sentBody()['url']);
    }

    public function testDisabledTrackingSendsNothing(): void
    {
        config(['supermetry.enabled' => false]);
        $this->fakeHttp();
        Route::get('/off', function () {
            Supermetry::track('x');
            return 'ok';
        });
        $this->get('/off')->assertOk();
        self::assertCount(0, $this->history);
    }
}
