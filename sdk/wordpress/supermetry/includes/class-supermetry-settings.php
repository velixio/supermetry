<?php
/**
 * Settings → Supermetry.
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Stores and renders the plugin settings (one autoloaded option array).
 */
final class Supermetry_Settings {

	const OPTION = 'supermetry_settings';
	const GROUP  = 'supermetry';
	const PAGE   = 'supermetry';

	/**
	 * Visitor address sources the admin may choose from. The server variable is
	 * only trustworthy when the matching proxy or CDN sits in front of the site.
	 */
	const IP_SOURCES = array( 'REMOTE_ADDR', 'HTTP_CF_CONNECTING_IP', 'HTTP_X_REAL_IP', 'HTTP_X_FORWARDED_FOR' );

	/**
	 * Hooks.
	 */
	public static function init() {
		add_action( 'init', array( __CLASS__, 'load_textdomain' ) );
		add_action( 'admin_init', array( __CLASS__, 'register' ) );
		add_action( 'admin_menu', array( __CLASS__, 'menu' ) );
		add_filter( 'plugin_action_links_' . plugin_basename( SUPERMETRY_FILE ), array( __CLASS__, 'action_links' ) );
	}

	/**
	 * Loads bundled translations (WordPress.org language packs load on their own).
	 */
	public static function load_textdomain() {
		load_plugin_textdomain( 'supermetry', false, dirname( plugin_basename( SUPERMETRY_FILE ) ) . '/languages' );
	}

	/**
	 * Default settings.
	 *
	 * @return array<string, mixed>
	 */
	public static function defaults() {
		return array(
			'host'          => 'https://app.supermetry.com',
			'site'          => '',
			'exclude_roles' => array( 'administrator' ),
			'hash'          => false,
			'outbound'      => true,
			'downloads'     => true,
			'forms'         => true,
			'proxy'         => false,
			'proxy_key'     => '',
			'ip_source'     => 'REMOTE_ADDR',
			'woocommerce'   => true,
		);
	}

	/**
	 * Current settings merged with the defaults.
	 *
	 * @return array<string, mixed>
	 */
	public static function get() {
		$stored = get_option( self::OPTION, array() );
		return array_merge( self::defaults(), is_array( $stored ) ? $stored : array() );
	}

	/**
	 * Supermetry origin (plus an optional path prefix), without a trailing slash.
	 *
	 * @return string
	 */
	public static function host() {
		$settings = self::get();
		return untrailingslashit( (string) $settings['host'] );
	}

	/**
	 * Whether the plugin has enough configuration to track.
	 *
	 * @return bool
	 */
	public static function configured() {
		$settings = self::get();
		return self::valid_site( (string) $settings['site'] ) && '' !== self::normalize_host( (string) $settings['host'] );
	}

	/**
	 * Site IDs are 12 lowercase letters and digits.
	 *
	 * @param string $site Site ID.
	 * @return bool
	 */
	public static function valid_site( $site ) {
		return 1 === preg_match( '/^[a-z0-9]{12}$/', $site );
	}

	/**
	 * Accepts an http(s) URL with no credentials, query or fragment and returns it
	 * without a trailing slash, or '' when invalid.
	 *
	 * @param string $raw URL.
	 * @return string
	 */
	public static function normalize_host( $raw ) {
		$raw   = trim( $raw );
		$parts = wp_parse_url( $raw );
		if ( ! is_array( $parts ) || empty( $parts['host'] ) || empty( $parts['scheme'] ) ) {
			return '';
		}
		$scheme = strtolower( $parts['scheme'] );
		if ( ( 'https' !== $scheme && 'http' !== $scheme ) || isset( $parts['user'] ) || isset( $parts['pass'] ) || isset( $parts['query'] ) || isset( $parts['fragment'] ) ) {
			return '';
		}
		$url = $scheme . '://' . strtolower( $parts['host'] );
		if ( isset( $parts['port'] ) ) {
			$url .= ':' . (int) $parts['port'];
		}
		if ( isset( $parts['path'] ) ) {
			$url .= untrailingslashit( $parts['path'] );
		}
		return esc_url_raw( $url, array( 'http', 'https' ) );
	}

	/**
	 * Registers the option with its sanitizer. options.php checks the nonce and the
	 * manage_options capability before calling it.
	 */
	public static function register() {
		register_setting(
			self::GROUP,
			self::OPTION,
			array(
				'type'              => 'array',
				'sanitize_callback' => array( __CLASS__, 'sanitize' ),
				'default'           => self::defaults(),
				'show_in_rest'      => false,
			)
		);
	}

	/**
	 * Sanitizes a settings form submission.
	 *
	 * @param mixed $input Raw input.
	 * @return array<string, mixed>
	 */
	public static function sanitize( $input ) {
		$current = self::get();
		if ( ! is_array( $input ) ) {
			return $current;
		}
		$output = self::defaults();

		$host = self::normalize_host( isset( $input['host'] ) ? (string) wp_unslash( $input['host'] ) : '' );
		if ( '' === $host ) {
			add_settings_error( self::OPTION, 'host', __( 'Enter the Supermetry URL as an http(s) address, for example https://app.supermetry.com.', 'supermetry' ) );
			$host = (string) $current['host'];
		}
		$output['host'] = $host;

		$site = isset( $input['site'] ) ? strtolower( sanitize_text_field( wp_unslash( $input['site'] ) ) ) : '';
		if ( '' !== $site && ! self::valid_site( $site ) ) {
			add_settings_error( self::OPTION, 'site', __( 'The site ID is 12 lowercase letters and digits. Copy it from Settings → Installation in Supermetry.', 'supermetry' ) );
			$site = (string) $current['site'];
		}
		$output['site'] = $site;

		$roles                   = array_keys( wp_roles()->get_names() );
		$selected                = isset( $input['exclude_roles'] ) && is_array( $input['exclude_roles'] ) ? array_map( 'sanitize_key', wp_unslash( $input['exclude_roles'] ) ) : array();
		$output['exclude_roles'] = array_values( array_intersect( $selected, $roles ) );

		foreach ( array( 'hash', 'outbound', 'downloads', 'forms', 'proxy', 'woocommerce' ) as $flag ) {
			$output[ $flag ] = ! empty( $input[ $flag ] );
		}

		$source              = isset( $input['ip_source'] ) ? sanitize_text_field( wp_unslash( $input['ip_source'] ) ) : 'REMOTE_ADDR';
		$output['ip_source'] = in_array( $source, self::IP_SOURCES, true ) ? $source : 'REMOTE_ADDR';

		// The proxy key is write-only: an empty field keeps the stored key.
		$key = isset( $input['proxy_key'] ) ? trim( sanitize_text_field( wp_unslash( $input['proxy_key'] ) ) ) : '';
		if ( ! empty( $input['proxy_key_clear'] ) ) {
			$output['proxy_key'] = '';
		} elseif ( '' === $key ) {
			$output['proxy_key'] = (string) $current['proxy_key'];
		} elseif ( preg_match( '/^[A-Za-z0-9_-]{8,200}$/', $key ) ) {
			$output['proxy_key'] = $key;
		} else {
			add_settings_error( self::OPTION, 'proxy_key', __( 'The proxy key may only contain letters, digits, "_" and "-".', 'supermetry' ) );
			$output['proxy_key'] = (string) $current['proxy_key'];
		}

		if ( $output['host'] !== $current['host'] || $output['site'] !== $current['site'] ) {
			Supermetry_Proxy::flush_cache();
		}
		return $output;
	}

	/**
	 * Adds Settings → Supermetry.
	 */
	public static function menu() {
		add_options_page(
			__( 'Supermetry', 'supermetry' ),
			__( 'Supermetry', 'supermetry' ),
			'manage_options',
			self::PAGE,
			array( __CLASS__, 'render' )
		);
	}

	/**
	 * Adds a Settings link on the Plugins screen.
	 *
	 * @param array<int|string, string> $links Links.
	 * @return array<int|string, string>
	 */
	public static function action_links( $links ) {
		$url = admin_url( 'options-general.php?page=' . self::PAGE );
		array_unshift( $links, '<a href="' . esc_url( $url ) . '">' . esc_html__( 'Settings', 'supermetry' ) . '</a>' );
		return $links;
	}

	/**
	 * Renders a checkbox row.
	 *
	 * @param array<string, mixed> $settings Settings.
	 * @param string               $key      Setting key.
	 * @param string               $label    Label.
	 * @param string               $help     Description.
	 */
	private static function checkbox( $settings, $key, $label, $help = '' ) {
		$id = 'supermetry-' . $key;
		echo '<label for="' . esc_attr( $id ) . '"><input type="checkbox" id="' . esc_attr( $id ) . '" name="' . esc_attr( self::OPTION . '[' . $key . ']' ) . '" value="1"' . checked( ! empty( $settings[ $key ] ), true, false ) . ' /> ' . esc_html( $label ) . '</label>';
		if ( '' !== $help ) {
			echo '<p class="description">' . esc_html( $help ) . '</p>';
		}
	}

	/**
	 * Renders the settings page.
	 */
	public static function render() {
		if ( ! current_user_can( 'manage_options' ) ) {
			return;
		}
		$settings = self::get();
		$name     = self::OPTION;
		?>
		<div class="wrap">
			<h1><?php esc_html_e( 'Supermetry', 'supermetry' ); ?></h1>
			<p><?php esc_html_e( 'Cookie-free analytics. Copy the site ID from Settings → Installation in your Supermetry dashboard.', 'supermetry' ); ?></p>
			<form method="post" action="options.php">
				<?php settings_fields( self::GROUP ); ?>
				<h2 class="title"><?php esc_html_e( 'Tracking', 'supermetry' ); ?></h2>
				<table class="form-table" role="presentation">
					<tr>
						<th scope="row"><label for="supermetry-host"><?php esc_html_e( 'Supermetry URL', 'supermetry' ); ?></label></th>
						<td>
							<input type="url" class="regular-text code" id="supermetry-host" name="<?php echo esc_attr( $name . '[host]' ); ?>" value="<?php echo esc_attr( $settings['host'] ); ?>" required />
							<p class="description"><?php esc_html_e( 'https://app.supermetry.com for Supermetry Cloud, or the URL of your own Supermetry server.', 'supermetry' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row"><label for="supermetry-site"><?php esc_html_e( 'Site ID', 'supermetry' ); ?></label></th>
						<td>
							<input type="text" class="regular-text code" id="supermetry-site" name="<?php echo esc_attr( $name . '[site]' ); ?>" value="<?php echo esc_attr( $settings['site'] ); ?>" pattern="[a-z0-9]{12}" maxlength="12" autocomplete="off" />
							<p class="description"><?php esc_html_e( 'The tracker is added to every public page once this is set.', 'supermetry' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row"><?php esc_html_e( 'Do not track', 'supermetry' ); ?></th>
						<td>
							<fieldset>
								<legend class="screen-reader-text"><?php esc_html_e( 'Roles that are not tracked', 'supermetry' ); ?></legend>
								<?php foreach ( wp_roles()->get_names() as $role => $label ) : ?>
									<label><input type="checkbox" name="<?php echo esc_attr( $name . '[exclude_roles][]' ); ?>" value="<?php echo esc_attr( $role ); ?>" <?php checked( in_array( $role, (array) $settings['exclude_roles'], true ) ); ?> /> <?php echo esc_html( translate_user_role( $label ) ); ?></label><br />
								<?php endforeach; ?>
								<p class="description"><?php esc_html_e( 'Logged-in users with these roles do not load the tracker and their WooCommerce orders are not sent.', 'supermetry' ); ?></p>
							</fieldset>
						</td>
					</tr>
					<tr>
						<th scope="row"><?php esc_html_e( 'Automatic events', 'supermetry' ); ?></th>
						<td>
							<fieldset>
								<?php
								self::checkbox( $settings, 'outbound', __( 'Outbound link clicks', 'supermetry' ) );
								echo '<br />';
								self::checkbox( $settings, 'downloads', __( 'File downloads', 'supermetry' ) );
								echo '<br />';
								self::checkbox( $settings, 'forms', __( 'Form submissions', 'supermetry' ) );
								?>
								<p class="description"><?php esc_html_e( 'These are switched on per site in Supermetry (Site settings → Tracking). Unchecking one here turns it off on this WordPress site even when it is on in Supermetry.', 'supermetry' ); ?></p>
							</fieldset>
						</td>
					</tr>
					<tr>
						<th scope="row"><?php esc_html_e( 'Hash routing', 'supermetry' ); ?></th>
						<td><?php self::checkbox( $settings, 'hash', __( 'Count changes of the URL #fragment as page views', 'supermetry' ) ); ?></td>
					</tr>
				</table>

				<h2 class="title"><?php esc_html_e( 'Ad blockers', 'supermetry' ); ?></h2>
				<table class="form-table" role="presentation">
					<tr>
						<th scope="row"><?php esc_html_e( 'First-party proxy', 'supermetry' ); ?></th>
						<td>
							<?php self::checkbox( $settings, 'proxy', __( 'Serve the tracker and receive events through this WordPress site', 'supermetry' ), __( 'Blockers that block the Supermetry domain then only see your own domain. Requests go through the WordPress REST API (/wp-json/sm/v1/), so they cost a PHP request each.', 'supermetry' ) ); ?>
						</td>
					</tr>
					<tr>
						<th scope="row"><label for="supermetry-proxy-key"><?php esc_html_e( 'Proxy key (Supermetry Cloud)', 'supermetry' ); ?></label></th>
						<td>
							<input type="password" class="regular-text code" id="supermetry-proxy-key" name="<?php echo esc_attr( $name . '[proxy_key]' ); ?>" value="" autocomplete="new-password" placeholder="<?php echo esc_attr( '' !== $settings['proxy_key'] ? __( 'Saved. Leave empty to keep it.', 'supermetry' ) : 'smpk_…' ); ?>" />
							<?php if ( '' !== $settings['proxy_key'] ) : ?>
								<br /><?php self::checkbox( $settings, 'proxy_key_clear', __( 'Remove the saved key', 'supermetry' ) ); ?>
							<?php endif; ?>
							<p class="description"><?php esc_html_e( 'Create it in Supermetry under Settings → Installation → Ad blockers. With the key, Supermetry Cloud counts each visitor\'s own address instead of this server\'s. Self-hosted: leave empty and add this server\'s address to SUPERMETRY_TRUSTED_PROXIES. The key is never sent to browsers.', 'supermetry' ); ?></p>
						</td>
					</tr>
					<tr>
						<th scope="row"><label for="supermetry-ip-source"><?php esc_html_e( 'Visitor address', 'supermetry' ); ?></label></th>
						<td>
							<select id="supermetry-ip-source" name="<?php echo esc_attr( $name . '[ip_source]' ); ?>">
								<option value="REMOTE_ADDR" <?php selected( $settings['ip_source'], 'REMOTE_ADDR' ); ?>><?php esc_html_e( 'Direct connection (REMOTE_ADDR)', 'supermetry' ); ?></option>
								<option value="HTTP_CF_CONNECTING_IP" <?php selected( $settings['ip_source'], 'HTTP_CF_CONNECTING_IP' ); ?>><?php esc_html_e( 'Cloudflare (CF-Connecting-IP)', 'supermetry' ); ?></option>
								<option value="HTTP_X_REAL_IP" <?php selected( $settings['ip_source'], 'HTTP_X_REAL_IP' ); ?>><?php esc_html_e( 'Reverse proxy (X-Real-IP)', 'supermetry' ); ?></option>
								<option value="HTTP_X_FORWARDED_FOR" <?php selected( $settings['ip_source'], 'HTTP_X_FORWARDED_FOR' ); ?>><?php esc_html_e( 'Load balancer (first X-Forwarded-For address)', 'supermetry' ); ?></option>
							</select>
							<p class="description"><?php esc_html_e( 'Where this server finds the visitor\'s address for proxied and server-side events. Only pick a header that your CDN or proxy always sets: browsers can send any of them.', 'supermetry' ); ?></p>
						</td>
					</tr>
				</table>

				<h2 class="title"><?php esc_html_e( 'WooCommerce', 'supermetry' ); ?></h2>
				<table class="form-table" role="presentation">
					<tr>
						<th scope="row"><?php esc_html_e( 'Purchases', 'supermetry' ); ?></th>
						<td>
							<?php self::checkbox( $settings, 'woocommerce', __( 'Send a Purchase event with the order total when the order-received page is shown', 'supermetry' ), __( 'Sent from this server, once per order, so ad blockers cannot drop it. The order currency must match the Supermetry site currency.', 'supermetry' ) ); ?>
						</td>
					</tr>
				</table>
				<?php submit_button(); ?>
			</form>
		</div>
		<?php
	}
}
