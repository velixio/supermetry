<?php

declare(strict_types=1);

namespace Supermetry\Tests;

use GuzzleHttp\Client as GuzzleClient;
use GuzzleHttp\Exception\ConnectException;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Middleware;
use GuzzleHttp\Psr7\Request;
use GuzzleHttp\Psr7\Response;
use PHPUnit\Framework\TestCase;
use Psr\Http\Message\RequestInterface;
use Supermetry\Client;
use Supermetry\SupermetryException;

final class ClientTest extends TestCase
{
    /** @var list<array{request: RequestInterface}> */
    private array $history = [];
    private MockHandler $mock;

    /**
     * @param array<string, mixed> $config
     * @param list<mixed> $responses
     */
    private function client(array $config = [], array $responses = [], ?array &$errors = null): Client
    {
        $this->history = [];
        $this->mock = new MockHandler($responses ?: array_fill(0, 10, new Response(202)));
        $stack = HandlerStack::create($this->mock);
        $stack->push(Middleware::history($this->history));
        $errors = [];
        $config += [
            'site' => 'site_123',
            'host' => 'https://analytics.supermetry.com/',
            'on_error' => function (SupermetryException $e) use (&$errors): void {
                $errors[] = $e;
            },
        ];
        return new Client($config, new GuzzleClient(['handler' => $stack]));
    }

    /** @return array<string, mixed> */
    private function body(int $index = 0): array
    {
        return json_decode((string) $this->history[$index]['request']->getBody(), true, 512, JSON_THROW_ON_ERROR);
    }

    public function testTrackSendsContractPayloadAndHeaders(): void
    {
        $client = $this->client();
        $client->track('Signup', ['plan' => 'pro', 'seats' => 3, 'trial' => true], [
            'url' => 'https://app.supermetry.com:8443/signup?x=1',
            'referrer' => 'https://google.com/',
            'user_agent' => 'Mozilla/5.0 Visitor',
            'ip' => '203.0.113.9',
            'revenue' => ['amount' => 49.9, 'currency' => 'usd'],
        ]);
        self::assertSame(1, $client->pendingCount());
        self::assertCount(0, $this->history, 'async sends are deferred until flush');
        $client->flush();

        self::assertCount(1, $this->history);
        $request = $this->history[0]['request'];
        self::assertSame('POST', $request->getMethod());
        self::assertSame('https://analytics.supermetry.com/api/public/v1/events', (string) $request->getUri());
        self::assertSame('application/json', $request->getHeaderLine('Content-Type'));
        self::assertSame('Mozilla/5.0 Visitor', $request->getHeaderLine('User-Agent'));
        self::assertSame('203.0.113.9', $request->getHeaderLine('X-Forwarded-For'));
        self::assertSame('https://app.supermetry.com:8443', $request->getHeaderLine('Origin'));

        $body = $this->body();
        self::assertSame(1, $body['v']);
        self::assertMatchesRegularExpression('/^[0-9a-f]{32}$/', $body['id']);
        self::assertEqualsWithDelta(microtime(true) * 1000, $body['ts'], 5000);
        self::assertSame('site_123', $body['site']);
        self::assertSame('Signup', $body['name']);
        self::assertSame('https://app.supermetry.com:8443/signup?x=1', $body['url']);
        self::assertSame('https://google.com/', $body['referrer']);
        self::assertSame(['plan' => 'pro', 'seats' => 3, 'trial' => true], $body['props']);
        self::assertSame(['amount' => '49.9', 'currency' => 'USD'], $body['revenue']);
        self::assertArrayNotHasKey('profile_id', $body);
        self::assertArrayNotHasKey('traits', $body);
    }

    public function testDefaultUserAgentIsNotAGenericHttpClient(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/']);
        $client->track('x');
        $client->flush();
        $ua = $this->history[0]['request']->getHeaderLine('User-Agent');
        self::assertSame('supermetry-php/0.1.0', $ua);
        self::assertStringNotContainsStringIgnoringCase('guzzle', $ua);
    }

    public function testPageviewUsesDefaultUrlAndDropsRevenue(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/home']);
        $client->pageview(null, [], ['revenue' => ['amount' => '1.00']]);
        $client->pageview('https://app.supermetry.com/about');
        $client->flush();
        self::assertSame('pageview', $this->body(0)['name']);
        self::assertSame('https://app.supermetry.com/home', $this->body(0)['url']);
        self::assertArrayNotHasKey('revenue', $this->body(0));
        self::assertSame('https://app.supermetry.com/about', $this->body(1)['url']);
    }

    public function testIdentifyStoresProfileAndOnlySendsTraitsOnIdentify(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/']);
        $client->identify('user-42', ['plan' => 'pro', 'age' => 30, 'bad' => ['nested'], str_repeat('k', 65) => 'x', 'long' => str_repeat('v', 257)]);
        $client->track('Clicked');
        $client->reset();
        $client->track('After reset');
        $client->flush();

        $identify = $this->body(0);
        self::assertSame('identify', $identify['name']);
        self::assertSame('user-42', $identify['profile_id']);
        self::assertSame(['plan' => 'pro', 'age' => '30'], $identify['traits']);

        $track = $this->body(1);
        self::assertSame('user-42', $track['profile_id']);
        self::assertArrayNotHasKey('traits', $track);

        self::assertArrayNotHasKey('profile_id', $this->body(2));
    }

    public function testIdentifyNullClearsProfileWithoutSending(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/']);
        $client->setProfileId('u1');
        $client->identify(null);
        $client->flush();
        self::assertNull($client->profileId());
        self::assertCount(0, $this->history);
    }

    public function testPropsAreCleanedAndTruncatedTo30Keys(): void
    {
        $props = [];
        for ($i = 0; $i < 40; $i++) {
            $props["k{$i}"] = $i;
        }
        $props = ['' => 'empty', 'arr' => [1], 'null' => null, 'nan' => NAN, 'long' => str_repeat('é', 1500)] + $props;
        $clean = Client::cleanProps($props);
        self::assertCount(30, $clean);
        self::assertArrayNotHasKey('', $clean);
        self::assertArrayNotHasKey('arr', $clean);
        self::assertArrayNotHasKey('null', $clean);
        self::assertArrayNotHasKey('nan', $clean);
        self::assertLessThanOrEqual(2000, strlen($clean['long']));
        self::assertTrue(mb_check_encoding($clean['long'], 'UTF-8'));
    }

    public function testRevenueValidation(): void
    {
        self::assertSame(['amount' => '49.90', 'currency' => 'EUR'], Client::cleanRevenue(['amount' => '49.90', 'currency' => 'EUR']));
        self::assertSame(['amount' => 10], Client::cleanRevenue(10));
        self::assertSame(['amount' => '0.000001'], Client::cleanRevenue(0.000001));
        self::assertNull(Client::cleanRevenue(['amount' => '1.1234567']));
        self::assertNull(Client::cleanRevenue(['amount' => '-1']));
        self::assertNull(Client::cleanRevenue(['amount' => 0]));
        self::assertNull(Client::cleanRevenue(['amount' => '5', 'currency' => 'EURO']));
    }

    public function testInvalidUrlIsReportedAndNotSent(): void
    {
        $client = $this->client([], [], $errors);
        $client->track('x');
        $client->track('x', [], ['url' => 'ftp://supermetry.com/']);
        $client->track('x', [], ['url' => 'https://user:pass@example.com/']);
        $client->track('x', [], ['url' => '/relative']);
        $client->flush();
        self::assertCount(0, $this->history);
        self::assertCount(4, $errors);
    }

    public function testInvalidNameIsReported(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/'], [], $errors);
        $client->track('');
        $client->track(str_repeat('n', 301));
        self::assertCount(2, $errors);
    }

    public function testOversizedBodyIsRejectedLocally(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/'], [], $errors);
        $props = [];
        for ($i = 0; $i < 10; $i++) {
            $props["k{$i}"] = str_repeat('x', 1000);
        }
        $client->track('big', $props);
        $client->flush();
        self::assertCount(0, $this->history);
        self::assertSame('event body exceeds 8 KB', $errors[0]->getMessage());
    }

    public function testHttpErrorsAreReportedNotThrown(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/'], [new Response(400), new Response(429, ['Retry-After' => '1'])], $errors);
        $client->track('a');
        $client->track('b');
        $client->flush();
        self::assertCount(2, $errors);
        self::assertSame(400, $errors[0]->status);
        self::assertFalse($errors[0]->retryable);
        self::assertSame(429, $errors[1]->status);
        self::assertTrue($errors[1]->retryable);
        self::assertSame('a', $errors[0]->payload['name']);
    }

    public function testNetworkErrorIsReportedAndRetryable(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/', 'async' => false], [
            new ConnectException('refused', new Request('POST', 'https://analytics.supermetry.com')),
        ], $errors);
        $client->track('a');
        self::assertCount(1, $errors);
        self::assertNull($errors[0]->status);
        self::assertTrue($errors[0]->retryable);
        self::assertSame($errors[0], $client->lastError());
    }

    public function testThrowModeThrows(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/', 'async' => false, 'throw' => true], [new Response(400)]);
        $this->expectException(SupermetryException::class);
        $client->track('a');
    }

    public function testSendNowRetryKeepsIdAndTimestamp(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/'], [new Response(503), new Response(202)]);
        $payload = $client->buildEvent('Purchase', [], ['id' => 'fixed_id-1', 'ts' => 1_700_000_000_000]);
        self::assertNotNull($payload);
        self::assertFalse($client->sendNow($payload));
        self::assertTrue($client->sendNow($payload));
        self::assertSame($this->body(0), $this->body(1));
        self::assertSame('fixed_id-1', $this->body(1)['id']);
        self::assertSame(1_700_000_000_000, $this->body(1)['ts']);
    }

    public function testPrivacySignalsAndCustomEndpoint(): void
    {
        $client = $this->client(['endpoint' => 'https://collector.supermetry.com/e', 'url' => 'https://app.supermetry.com/', 'send_origin' => false]);
        $client->track('a', [], ['dnt' => true, 'gpc' => true]);
        $client->flush();
        $request = $this->history[0]['request'];
        self::assertSame('https://collector.supermetry.com/e', (string) $request->getUri());
        self::assertSame('1', $request->getHeaderLine('DNT'));
        self::assertSame('1', $request->getHeaderLine('Sec-GPC'));
        self::assertFalse($request->hasHeader('Origin'));
    }

    public function testDisabledClientIsNoop(): void
    {
        foreach ([['enabled' => false], ['enabled' => 'false'], ['site' => ''], ['host' => null]] as $config) {
            $client = $this->client($config + ['url' => 'https://app.supermetry.com/']);
            $client->track('a');
            $client->identify('u');
            $client->flush();
            self::assertFalse($client->isEnabled());
            self::assertCount(0, $this->history);
        }
    }

    public function testDestructorFlushes(): void
    {
        $client = $this->client(['url' => 'https://app.supermetry.com/']);
        $client->track('a');
        unset($client);
        self::assertCount(1, $this->history);
    }
}
