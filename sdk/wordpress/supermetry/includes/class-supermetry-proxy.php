<?php
/**
 * Optional first-party proxy through the WordPress REST API.
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Serves the tracker and forwards events (and session replay uploads) to the
 * configured Supermetry host, so ad blockers that block the Supermetry domain
 * only see the site's own domain. Every upstream URL is the configured host plus
 * a fixed path; visitor input only fills validated path segments and queries.
 */
final class Supermetry_Proxy {

	/**
	 * A short, neutral REST namespace: /wp-json/sm/v1/.
	 */
	const NAMESPACE_V1 = 'sm/v1';

	const CACHE_PREFIX   = 'supermetry_js_';
	const MAX_EVENT_BODY = 8192;
	const MAX_UPLOAD     = 2097152;

	/**
	 * Hooks.
	 */
	public static function init() {
		add_action( 'rest_api_init', array( __CLASS__, 'routes' ) );
		add_filter( 'rest_pre_serve_request', array( __CLASS__, 'serve_raw' ), 10, 4 );
	}

	/**
	 * Directory per-site scripts are served from on the Supermetry host
	 * (the directory of SUPERMETRY_TRACKER_PATH, "/js" by default).
	 *
	 * @return string
	 */
	public static function script_dir() {
		/**
		 * Filters the Supermetry script directory, e.g. "/js".
		 *
		 * @param string $dir Directory with a leading slash and no trailing slash.
		 */
		$dir = (string) apply_filters( 'supermetry_script_dir', '/js' );
		$dir = '/' . trim( $dir, '/' );
		return preg_match( '#^(/[A-Za-z0-9._~-]+)+$#', $dir ) ? $dir : '/js';
	}

	/**
	 * Whether the proxy is switched on and configured.
	 *
	 * @return bool
	 */
	public static function enabled() {
		$settings = Supermetry_Settings::get();
		return ! empty( $settings['proxy'] ) && Supermetry_Settings::configured();
	}

	/**
	 * Registers the proxy routes when the proxy is on.
	 */
	public static function routes() {
		if ( ! self::enabled() ) {
			return;
		}
		$public = '__return_true';
		register_rest_route(
			self::NAMESPACE_V1,
			'/js/(?P<site>[a-z0-9]{12})\.js',
			array(
				'methods'             => WP_REST_Server::READABLE,
				'callback'            => array( __CLASS__, 'script' ),
				'permission_callback' => $public,
				'args'                => array(
					'm'      => array(
						'type'    => 'string',
						'pattern' => '^[a-z]{1,8}(,[a-z]{1,8}){0,4}$',
					),
					'module' => array(
						'type'    => 'string',
						'pattern' => '^[a-z]{1,16}(,[a-z]{1,16}){0,4}$',
					),
					'full'   => array(
						'type' => 'string',
						'enum' => array( '1' ),
					),
				),
			)
		);
		register_rest_route(
			self::NAMESPACE_V1,
			Supermetry_Client::EVENTS_PATH,
			array(
				'methods'             => WP_REST_Server::CREATABLE,
				'callback'            => array( __CLASS__, 'event' ),
				'permission_callback' => $public,
			)
		);
		register_rest_route(
			self::NAMESPACE_V1,
			'/replay\.js',
			array(
				'methods'             => WP_REST_Server::READABLE,
				'callback'            => array( __CLASS__, 'replay_script' ),
				'permission_callback' => $public,
			)
		);
		register_rest_route(
			self::NAMESPACE_V1,
			'/api/replay/(?P<op>start|chunk)',
			array(
				'methods'             => WP_REST_Server::CREATABLE,
				'callback'            => array( __CLASS__, 'replay_upload' ),
				'permission_callback' => $public,
			)
		);
		register_rest_route(
			self::NAMESPACE_V1,
			'/api/replay/css/(?P<hash>[a-f0-9]{64})',
			array(
				'methods'             => 'PUT',
				'callback'            => array( __CLASS__, 'replay_upload' ),
				'permission_callback' => $public,
			)
		);
	}

	/**
	 * GET /sm/v1/js/<site>.js: the site's tracker, cached in a transient.
	 *
	 * @param WP_REST_Request $request Request.
	 * @return WP_REST_Response
	 */
	public static function script( WP_REST_Request $request ) {
		$settings = Supermetry_Settings::get();
		$site     = (string) $request['site'];
		// Only this site's script: the proxy is not an open relay for other sites.
		if ( $site !== $settings['site'] ) {
			return self::raw( 404, '', 'text/plain' );
		}
		$query = array();
		foreach ( array( 'm', 'module', 'full' ) as $name ) {
			if ( null !== $request[ $name ] && '' !== $request[ $name ] ) {
				$query[ $name ] = (string) $request[ $name ];
			}
		}
		return self::cached_asset( self::script_dir() . '/' . $site . '.js', $query, $request );
	}

	/**
	 * GET /sm/v1/replay.js: the session replay recorder.
	 *
	 * @param WP_REST_Request $request Request.
	 * @return WP_REST_Response
	 */
	public static function replay_script( WP_REST_Request $request ) {
		return self::cached_asset( '/replay.js', array(), $request );
	}

	/**
	 * Serves a GET asset from the transient cache, refreshing it from Supermetry.
	 *
	 * @param string                $path    Upstream path.
	 * @param array<string, string> $query   Upstream query.
	 * @param WP_REST_Request       $request Request.
	 * @return WP_REST_Response
	 */
	private static function cached_asset( $path, array $query, WP_REST_Request $request ) {
		$key    = self::CACHE_PREFIX . md5( get_option( self::CACHE_PREFIX . 'generation', '' ) . '|' . Supermetry_Settings::host() . '|' . $path . '|' . wp_json_encode( $query ) );
		$cached = get_transient( $key );
		if ( ! is_array( $cached ) || ! isset( $cached['body'], $cached['etag'] ) ) {
			$upstream = Supermetry_Client::forward( 'GET', $path, $query, null, array(), false );
			if ( is_wp_error( $upstream ) || 200 !== $upstream['status'] ) {
				$status = is_wp_error( $upstream ) ? 502 : ( 404 === $upstream['status'] ? 404 : 502 );
				return self::raw( $status, '', 'text/plain', array( 'Cache-Control' => 'no-store' ) );
			}
			$cached = array(
				'body' => $upstream['body'],
				'etag' => '"' . substr( md5( $upstream['body'] ), 0, 16 ) . '"',
			);
			/**
			 * Filters how long (seconds) the proxied tracker is cached. Site setting
			 * changes in Supermetry reach visitors after at most this long.
			 *
			 * @param int $ttl Seconds.
			 */
			set_transient( $key, $cached, max( 60, (int) apply_filters( 'supermetry_script_cache_ttl', 5 * MINUTE_IN_SECONDS ) ) );
		}
		$headers = array(
			'Cache-Control' => 'public, max-age=300',
			'ETag'          => $cached['etag'],
		);
		if ( trim( (string) $request->get_header( 'if_none_match' ) ) === $cached['etag'] ) {
			return self::raw( 304, '', 'application/javascript', $headers );
		}
		return self::raw( 200, (string) $cached['body'], 'application/javascript; charset=utf-8', $headers );
	}

	/**
	 * POST /sm/v1/api/public/v1/events: forwards one tracker event.
	 *
	 * @param WP_REST_Request $request Request.
	 * @return WP_REST_Response
	 */
	public static function event( WP_REST_Request $request ) {
		$body = (string) $request->get_body();
		if ( strlen( $body ) > self::MAX_EVENT_BODY ) {
			return self::raw( 413, '', 'text/plain' );
		}
		$payload  = json_decode( $body, true );
		$settings = Supermetry_Settings::get();
		if ( ! is_array( $payload ) || ! isset( $payload['site'] ) || $payload['site'] !== $settings['site'] ) {
			return self::raw( 400, '', 'text/plain' );
		}
		$type     = (string) $request->get_header( 'content_type' );
		$upstream = Supermetry_Client::forward(
			'POST',
			Supermetry_Client::EVENTS_PATH,
			array(),
			$body,
			self::origin_headers( $request, '' !== $type && 0 === stripos( $type, 'application/json' ) ? 'application/json' : 'text/plain;charset=UTF-8' )
		);
		return self::relay( $upstream );
	}

	/**
	 * Forwards a session replay request (start, chunk or stylesheet).
	 *
	 * @param WP_REST_Request $request Request.
	 * @return WP_REST_Response
	 */
	public static function replay_upload( WP_REST_Request $request ) {
		$body = (string) $request->get_body();
		if ( strlen( $body ) > self::MAX_UPLOAD ) {
			return self::raw( 413, '', 'text/plain' );
		}
		if ( isset( $request['hash'] ) ) {
			$path = '/api/replay/css/' . $request['hash'];
			$type = 'application/gzip';
		} else {
			$path = 'start' === $request['op'] ? '/api/replay/start' : '/api/replay/chunk';
			$type = 'start' === $request['op'] ? 'application/json' : 'application/octet-stream';
		}
		$query = array();
		foreach ( $request->get_query_params() as $name => $value ) {
			if ( is_string( $name ) && is_string( $value ) && preg_match( '/^[a-z]{1,8}$/', $name ) && strlen( $value ) <= 256 ) {
				$query[ $name ] = $value;
			}
		}
		unset( $query['rest_route'] );
		$upstream = Supermetry_Client::forward( $request->get_method(), $path, $query, $body, self::origin_headers( $request, $type ) );
		return self::relay( $upstream );
	}

	/**
	 * Content type plus the browser's Origin and Referer (needed for strict origin).
	 *
	 * @param WP_REST_Request $request Request.
	 * @param string          $type    Content type.
	 * @return array<string, string>
	 */
	private static function origin_headers( WP_REST_Request $request, $type ) {
		$headers = array( 'Content-Type' => $type );
		foreach ( array(
			'origin'  => 'Origin',
			'referer' => 'Referer',
		) as $name => $header ) {
			$value = (string) $request->get_header( $name );
			if ( '' !== $value && strlen( $value ) <= 2000 ) {
				$headers[ $header ] = esc_url_raw( $value, array( 'http', 'https' ) );
			}
		}
		return array_filter( $headers );
	}

	/**
	 * Turns an upstream answer into a REST response with the same status and body.
	 *
	 * @param array{status:int, body:string, headers:array<string,string>}|WP_Error $upstream Upstream answer.
	 * @return WP_REST_Response
	 */
	private static function relay( $upstream ) {
		if ( is_wp_error( $upstream ) ) {
			return self::raw( 502, '', 'text/plain' );
		}
		$extra = array( 'Cache-Control' => 'no-store' );
		if ( isset( $upstream['headers']['retry-after'] ) ) {
			$extra['Retry-After'] = $upstream['headers']['retry-after'];
		}
		$type = isset( $upstream['headers']['content-type'] ) ? $upstream['headers']['content-type'] : 'text/plain';
		return self::raw( $upstream['status'] > 0 ? $upstream['status'] : 502, $upstream['body'], $type, $extra );
	}

	/**
	 * A REST response whose body is sent as-is (see serve_raw()).
	 *
	 * @param int                   $status  HTTP status.
	 * @param string                $body    Body.
	 * @param string                $type    Content type.
	 * @param array<string, string> $headers Extra headers.
	 * @return WP_REST_Response
	 */
	private static function raw( $status, $body, $type, array $headers = array() ) {
		$response = new WP_REST_Response( $body, $status );
		$response->header( 'Content-Type', $type );
		$response->header( 'X-Content-Type-Options', 'nosniff' );
		foreach ( $headers as $name => $value ) {
			$response->header( $name, $value );
		}
		return $response;
	}

	/**
	 * Prints proxy responses (string bodies under our namespace) verbatim instead of
	 * JSON-encoding them. WP_Error responses keep the normal JSON output.
	 *
	 * @param bool             $served  Whether the request was already served.
	 * @param WP_HTTP_Response $result  Response.
	 * @param WP_REST_Request  $request Request.
	 * @param WP_REST_Server   $server  Server.
	 * @return bool
	 */
	public static function serve_raw( $served, $result, $request, $server ) {
		unset( $server );
		if ( $served || ! $result instanceof WP_HTTP_Response || ! is_string( $result->get_data() ) ) {
			return $served;
		}
		if ( 0 !== strpos( (string) $request->get_route(), '/' . self::NAMESPACE_V1 . '/' ) ) {
			return $served;
		}
		$body = $result->get_data();
		if ( 'HEAD' !== $request->get_method() && '' !== $body ) {
			echo $body; // phpcs:ignore WordPress.Security.EscapeOutput.OutputNotEscaped -- Verbatim JavaScript or API body from the configured Supermetry host, served with its own Content-Type and nosniff.
		}
		return true;
	}

	/**
	 * Invalidates every cached proxy asset by moving to a new cache generation
	 * (works with and without a persistent object cache).
	 */
	public static function flush_cache() {
		update_option( self::CACHE_PREFIX . 'generation', (string) wp_rand(), true );
	}
}
