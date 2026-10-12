<?php

declare(strict_types=1);

namespace Supermetry\Laravel;

use Illuminate\Contracts\Bus\Dispatcher;
use Illuminate\Contracts\Foundation\Application;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Blade;
use Illuminate\Support\Facades\Log;
use Illuminate\Support\ServiceProvider;
use Supermetry\Client;
use Supermetry\Laravel\Http\Middleware\TrackPageview;
use Supermetry\SupermetryException;

class SupermetryServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__ . '/../../config/supermetry.php', 'supermetry');

        // Scoped: one client (and one identified profile) per request / queued job, Octane-safe.
        $this->app->scoped(Client::class, function (Application $app): Client {
            $config = (array) $app['config']->get('supermetry', []);
            $config['on_error'] = function (SupermetryException $error) use ($config): void {
                if (filter_var($config['log_errors'] ?? true, FILTER_VALIDATE_BOOLEAN)) {
                    Log::warning('supermetry: ' . $error->getMessage(), ['status' => $error->status]);
                }
            };
            return new Client($config);
        });

        $this->app->scoped(Tracker::class, function (Application $app): Tracker {
            $requestResolver = static function () use ($app): ?Request {
                if (!$app->bound('request')) {
                    return null;
                }
                // Console commands have a synthetic http://localhost request; ignore it.
                if ($app->runningInConsole() && !$app->runningUnitTests()) {
                    return null;
                }
                $request = $app->make('request');
                return $request instanceof Request ? $request : null;
            };
            return new Tracker(
                $app->make(Client::class),
                $requestResolver,
                $app->bound(Dispatcher::class) ? $app->make(Dispatcher::class) : null,
                (array) $app['config']->get('supermetry', []),
            );
        });
        $this->app->alias(Tracker::class, 'supermetry');
    }

    public function boot(): void
    {
        if ($this->app->runningInConsole()) {
            $this->publishes([
                __DIR__ . '/../../config/supermetry.php' => $this->app->configPath('supermetry.php'),
            ], 'supermetry-config');
        }

        $this->app['router']->aliasMiddleware('supermetry.pageview', TrackPageview::class);

        Blade::directive('supermetry', static function (string $expression): string {
            $arguments = trim($expression) === '' ? '[]' : $expression;
            return "<?php echo app(\\Supermetry\\Laravel\\Tracker::class)->script({$arguments}); ?>";
        });

        // Settle asynchronous sends after the response has been sent.
        $this->app->terminating(function (): void {
            if ($this->app->resolved(Client::class)) {
                try {
                    $this->app->make(Client::class)->flush();
                } catch (\Throwable) {
                    // flush() only throws when `throw` is enabled; never break termination.
                }
            }
        });
    }
}
