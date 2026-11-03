<?php
/**
 * Plugin Name:       Supermetry Analytics
 * Plugin URI:        https://github.com/velixio/supermetry/tree/main/sdk/wordpress
 * Description:       Cookie-free, privacy-friendly analytics with Supermetry. Adds the tracker, an optional first-party proxy against ad blockers, and WooCommerce purchase tracking.
 * Version:           0.1.0
 * Requires at least: 6.0
 * Requires PHP:      7.4
 * Author:            Supermetry
 * Author URI:        https://supermetry.com
 * License:           GPL-2.0-or-later
 * License URI:       https://www.gnu.org/licenses/gpl-2.0.html
 * Text Domain:       supermetry
 * Domain Path:       /languages
 * WC requires at least: 7.0
 * WC tested up to:   9.4
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

define( 'SUPERMETRY_VERSION', '0.1.0' );
define( 'SUPERMETRY_FILE', __FILE__ );
define( 'SUPERMETRY_DIR', plugin_dir_path( __FILE__ ) );

require_once SUPERMETRY_DIR . 'includes/class-supermetry-settings.php';
require_once SUPERMETRY_DIR . 'includes/class-supermetry-client.php';
require_once SUPERMETRY_DIR . 'includes/class-supermetry-tracker.php';
require_once SUPERMETRY_DIR . 'includes/class-supermetry-proxy.php';
require_once SUPERMETRY_DIR . 'includes/class-supermetry-woocommerce.php';

Supermetry_Settings::init();
Supermetry_Tracker::init();
Supermetry_Proxy::init();
Supermetry_WooCommerce::init();

add_action(
	'before_woocommerce_init',
	static function () {
		if ( class_exists( '\Automattic\WooCommerce\Utilities\FeaturesUtil' ) ) {
			\Automattic\WooCommerce\Utilities\FeaturesUtil::declare_compatibility( 'custom_order_tables', SUPERMETRY_FILE, true );
		}
	}
);
