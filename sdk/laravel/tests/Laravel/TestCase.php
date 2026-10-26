<?php

declare(strict_types=1);

namespace Supermetry\Tests\Laravel;

use GuzzleHttp\Client as GuzzleClient;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Middleware;
use GuzzleHttp\Psr7\Response;
use Orchestra\Testbench\TestCase as Orchestra;
use Supermetry\Client;
use Supermetry\Laravel\Facades\Supermetry;
use Supermetry\Laravel\SupermetryServiceProvider;

abstract class TestCase extends Orchestra
{
    /** @var array<int, array<string, mixed>> */
    protected array $history = [];

    protected function getPackageProviders($app): array
    {
        return [SupermetryServiceProvider::class];
    }

    protected function getPackageAliases($app): array
    {
        return ['Supermetry' => Supermetry::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set('supermetry.site', 'site_123');
        $app['config']->set('supermetry.host', 'https://analytics.supermetry.com');
        $app['config']->set('supermetry.log_errors', false);
    }

    /** Rebind the client so every request goes to a Guzzle MockHandler (always 202). */
    protected function fakeHttp(): void
    {
        $this->history = [];
        $history = &$this->history;
        $this->app->scoped(Client::class, function ($app) use (&$history): Client {
            $stack = HandlerStack::create(new MockHandler(array_fill(0, 20, new Response(202))));
            $stack->push(Middleware::history($history));
            return new Client((array) $app['config']->get('supermetry'), new GuzzleClient(['handler' => $stack]));
        });
        $this->app->forgetScopedInstances();
    }

    /** @return array<string, mixed> */
    protected function sentBody(int $index = 0): array
    {
        return json_decode((string) $this->history[$index]['request']->getBody(), true, 512, JSON_THROW_ON_ERROR);
    }

    protected function sentHeader(int $index, string $name): string
    {
        return $this->history[$index]['request']->getHeaderLine($name);
    }
}
