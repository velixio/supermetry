<?php

declare(strict_types=1);

namespace Supermetry\Laravel\Facades;

use Illuminate\Support\Facades\Facade;
use Supermetry\Laravel\Tracker;

/**
 * @method static void track(string $name, array $props = [], array $options = [])
 * @method static void pageview(?string $url = null, array $props = [], array $options = [])
 * @method static void identify(?string $profileId, array $traits = [], array $options = [])
 * @method static void reset()
 * @method static void flush()
 * @method static string script(array $options = [])
 * @method static bool enabled()
 * @method static \Supermetry\Client client()
 *
 * @see Tracker
 */
class Supermetry extends Facade
{
    protected static function getFacadeAccessor(): string
    {
        return Tracker::class;
    }
}
