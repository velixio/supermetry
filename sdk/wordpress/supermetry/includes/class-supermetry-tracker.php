<?php
/**
 * Adds the tracker script tag to public pages.
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Enqueues the Supermetry tracker with its data-* options.
 */
final class Supermetry_Tracker {

	const HANDLE = 'supermetry';

	/**
	 * Hooks.
	 */
	public static function init() {
		add_action( 'wp_enqueue_scripts', array( __CLASS__, 'enqueue' ) );
		add_filter( 'script_loader_tag', array( __CLASS__, 'script_tag' ), 10, 2 );
	}

	/**
	 * Whether the current visitor should be tracked.
	 *
	 * @return bool
	 */
	public static function should_track() {
		$track = Supermetry_Settings::configured() && ! is_admin() && ! is_preview() && ! is_customize_preview() && ! self::excluded_user();
		/**
		 * Filters whether Supermetry tracks the current request.
		 *
		 * @param bool $track Whether to track.
		 */
		return (bool) apply_filters( 'supermetry_should_track', $track );
	}

	/**
	 * Whether the logged-in user has an excluded role.
	 *
	 * @return bool
	 */
	public static function excluded_user() {
		if ( ! is_user_logged_in() ) {
			return false;
		}
		$settings = Supermetry_Settings::get();
		$user     = wp_get_current_user();
		return array() !== array_intersect( (array) $user->roles, (array) $settings['exclude_roles'] );
	}

	/**
	 * The script URL: the site's script on Supermetry, or through the local proxy.
	 *
	 * @return string
	 */
	public static function script_url() {
		$settings = Supermetry_Settings::get();
		$file     = $settings['site'] . '.js';
		if ( ! empty( $settings['proxy'] ) ) {
			return rest_url( Supermetry_Proxy::NAMESPACE_V1 . '/js/' . $file );
		}
		return Supermetry_Settings::host() . Supermetry_Proxy::script_dir() . '/' . $file;
	}

	/**
	 * The data-* attributes for the script tag.
	 *
	 * @return array<string, string>
	 */
	public static function attributes() {
		$settings   = Supermetry_Settings::get();
		$attributes = array( 'data-site' => (string) $settings['site'] );
		if ( ! empty( $settings['proxy'] ) ) {
			$attributes['data-api'] = rest_url( Supermetry_Proxy::NAMESPACE_V1 . Supermetry_Client::EVENTS_PATH );
		}
		if ( ! empty( $settings['hash'] ) ) {
			$attributes['data-hash'] = 'true';
		}
		// The dashboard decides which automatic events exist; the snippet can only turn them off.
		foreach ( array( 'outbound', 'downloads', 'forms' ) as $feature ) {
			if ( empty( $settings[ $feature ] ) ) {
				$attributes[ 'data-' . $feature ] = 'false';
			}
		}
		// Keep script optimizers (Cloudflare Rocket Loader, LiteSpeed) from deferring or
		// bundling it: the tracker reads its own <script> tag.
		$attributes['data-cfasync']     = 'false';
		$attributes['data-no-optimize'] = '1';
		/**
		 * Filters the attributes of the Supermetry script tag, e.g. to add
		 * data-consent="required" or data-download-ext.
		 *
		 * @param array<string, string> $attributes Attribute name => value.
		 */
		return (array) apply_filters( 'supermetry_script_attributes', $attributes );
	}

	/**
	 * Registers and enqueues the tracker in <head>.
	 */
	public static function enqueue() {
		if ( ! self::should_track() ) {
			return;
		}
		// A null version keeps "?ver=" off the URL; the server sends its own cache headers.
		wp_register_script( self::HANDLE, self::script_url(), array(), null, false ); // phpcs:ignore WordPress.WP.EnqueuedResourceParameters.MissingVersion
		if ( version_compare( get_bloginfo( 'version' ), '6.3', '>=' ) ) {
			wp_script_add_data( self::HANDLE, 'strategy', 'defer' );
		}
		wp_enqueue_script( self::HANDLE );
	}

	/**
	 * Adds defer and the data-* attributes to the tracker's tag.
	 *
	 * @param string $tag    Script HTML.
	 * @param string $handle Script handle.
	 * @return string
	 */
	public static function script_tag( $tag, $handle ) {
		if ( self::HANDLE !== $handle ) {
			return $tag;
		}
		$extra = '';
		if ( ! preg_match( '/<script\b[^>]*\sdefer[\s=>]/i', $tag ) ) {
			$extra .= ' defer';
		}
		foreach ( self::attributes() as $name => $value ) {
			$name = strtolower( (string) $name );
			if ( ! preg_match( '/^data-[a-z0-9-]+$/', $name ) ) {
				continue;
			}
			$extra .= ' ' . $name . '="' . esc_attr( (string) $value ) . '"';
		}
		return (string) preg_replace( '/<script\b/i', '<script' . $extra, $tag, 1 );
	}
}
