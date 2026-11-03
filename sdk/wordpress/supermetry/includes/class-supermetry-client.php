<?php
/**
 * Server-side requests to Supermetry.
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Sends events and proxied requests to the configured Supermetry host only.
 */
final class Supermetry_Client {

	const EVENTS_PATH = '/api/public/v1/events';
	const USER_AGENT  = 'Supermetry-WordPress/' . SUPERMETRY_VERSION;

	/**
	 * The visitor's IP address from the configured source, or '' when unknown.
	 *
	 * @return string
	 */
	public static function visitor_ip() {
		$settings = Supermetry_Settings::get();
		$source   = in_array( $settings['ip_source'], Supermetry_Settings::IP_SOURCES, true ) ? $settings['ip_source'] : 'REMOTE_ADDR';
		$raw      = isset( $_SERVER[ $source ] ) ? sanitize_text_field( wp_unslash( $_SERVER[ $source ] ) ) : '';
		if ( 'HTTP_X_FORWARDED_FOR' === $source ) {
			$raw = trim( explode( ',', $raw )[0] );
		}
		$ip = filter_var( $raw, FILTER_VALIDATE_IP );
		return false === $ip ? '' : (string) $ip;
	}

	/**
	 * The visitor's User-Agent, or the plugin's own when there is none.
	 *
	 * @return string
	 */
	public static function visitor_user_agent() {
		$ua = isset( $_SERVER['HTTP_USER_AGENT'] ) ? sanitize_text_field( wp_unslash( $_SERVER['HTTP_USER_AGENT'] ) ) : '';
		return '' === $ua ? self::USER_AGENT : substr( $ua, 0, 512 );
	}

	/**
	 * Headers that tell Supermetry who the visitor is and which privacy signals
	 * they sent. Cookies are never forwarded.
	 *
	 * @return array<string, string>
	 */
	public static function visitor_headers() {
		$settings = Supermetry_Settings::get();
		$headers  = array( 'User-Agent' => self::visitor_user_agent() );
		$ip       = self::visitor_ip();
		if ( '' !== $ip ) {
			// Self-hosted servers trust this from SUPERMETRY_TRUSTED_PROXIES.
			$headers['X-Forwarded-For'] = $ip;
			if ( '' !== $settings['proxy_key'] ) {
				// Supermetry Cloud trusts the address only alongside the site's proxy key.
				$headers['X-Supermetry-Proxy-Key'] = (string) $settings['proxy_key'];
				$headers['X-Supermetry-Client-IP'] = $ip;
			}
		}
		$forward = array(
			'HTTP_DNT'             => 'DNT',
			'HTTP_SEC_GPC'         => 'Sec-GPC',
			'HTTP_ACCEPT_LANGUAGE' => 'Accept-Language',
		);
		foreach ( $forward as $server => $header ) {
			if ( isset( $_SERVER[ $server ] ) ) {
				$headers[ $header ] = substr( sanitize_text_field( wp_unslash( $_SERVER[ $server ] ) ), 0, 256 );
			}
		}
		return $headers;
	}

	/**
	 * A stable event ID (dedup key) for a value, 1–64 chars of [A-Za-z0-9_-].
	 *
	 * @param string $seed Seed, e.g. "woocommerce-order-42".
	 * @return string
	 */
	public static function event_id( $seed ) {
		return 'wp' . substr( hash( 'sha256', home_url() . '|' . $seed ), 0, 40 );
	}

	/**
	 * Sends one event from the server, forwarding the current visitor.
	 *
	 * @param string               $name    Event name.
	 * @param string               $url     Absolute page URL on an allowed host.
	 * @param array<string, mixed> $props   Event properties (scalars).
	 * @param array<string, mixed> $options revenue, id, referrer.
	 * @return bool Whether Supermetry accepted the event (2xx).
	 */
	public static function send_event( $name, $url, array $props = array(), array $options = array() ) {
		$settings = Supermetry_Settings::get();
		if ( ! Supermetry_Settings::configured() ) {
			return false;
		}
		$payload = array(
			'v'     => 1,
			'id'    => isset( $options['id'] ) ? (string) $options['id'] : self::event_id( wp_generate_uuid4() ),
			'ts'    => (int) round( microtime( true ) * 1000 ),
			'site'  => (string) $settings['site'],
			'name'  => substr( $name, 0, 300 ),
			'url'   => $url,
			'props' => array_slice( array_filter( $props, 'is_scalar' ), 0, 30, true ),
		);
		if ( isset( $options['referrer'] ) ) {
			$payload['referrer'] = (string) $options['referrer'];
		}
		if ( isset( $options['revenue'] ) && is_array( $options['revenue'] ) ) {
			$payload['revenue'] = $options['revenue'];
		}
		if ( empty( $payload['props'] ) ) {
			$payload['props'] = new stdClass();
		}

		$headers                 = self::visitor_headers();
		$headers['Content-Type'] = 'application/json';
		$headers['Referer']      = $url;
		// Sites with strict origin accept server events that name their own origin.
		$origin = wp_parse_url( $url );
		if ( is_array( $origin ) && isset( $origin['scheme'], $origin['host'] ) ) {
			$headers['Origin'] = $origin['scheme'] . '://' . $origin['host'] . ( isset( $origin['port'] ) ? ':' . $origin['port'] : '' );
		}

		$response = wp_remote_post(
			Supermetry_Settings::host() . self::EVENTS_PATH,
			array(
				'timeout'     => 3,
				'redirection' => 0,
				'headers'     => $headers,
				'body'        => wp_json_encode( $payload ),
				'data_format' => 'body',
			)
		);
		if ( is_wp_error( $response ) ) {
			return false;
		}
		$code = (int) wp_remote_retrieve_response_code( $response );
		return $code >= 200 && $code < 300;
	}

	/**
	 * Forwards a request to a fixed path on the configured Supermetry host. The
	 * caller builds $path from constants and validated segments, so the upstream
	 * can never be chosen by the visitor.
	 *
	 * @param string                $method  HTTP method.
	 * @param string                $path    Absolute path on the Supermetry host.
	 * @param array<string, string> $query   Query parameters.
	 * @param string|null           $body    Request body.
	 * @param array<string, string> $headers Extra headers.
	 * @param bool                  $visitor Whether to forward the visitor's identity headers.
	 * @return array{status:int, body:string, headers:array<string,string>}|WP_Error
	 */
	public static function forward( $method, $path, array $query = array(), $body = null, array $headers = array(), $visitor = true ) {
		$url = Supermetry_Settings::host() . $path;
		if ( ! empty( $query ) ) {
			$url .= '?' . http_build_query( $query, '', '&', PHP_QUERY_RFC3986 );
		}
		$args = array(
			'method'      => $method,
			'timeout'     => 5,
			'redirection' => 0,
			'headers'     => array_merge( $visitor ? self::visitor_headers() : array( 'User-Agent' => self::USER_AGENT ), $headers ),
		);
		if ( null !== $body ) {
			$args['body'] = $body;
		}
		$response = wp_remote_request( $url, $args );
		if ( is_wp_error( $response ) ) {
			return $response;
		}
		$out = array();
		foreach ( array( 'content-type', 'etag', 'retry-after', 'cache-control' ) as $name ) {
			$value = wp_remote_retrieve_header( $response, $name );
			if ( is_array( $value ) ) {
				$value = end( $value );
			}
			if ( '' !== (string) $value ) {
				$out[ $name ] = (string) $value;
			}
		}
		return array(
			'status'  => (int) wp_remote_retrieve_response_code( $response ),
			'body'    => (string) wp_remote_retrieve_body( $response ),
			'headers' => $out,
		);
	}
}
