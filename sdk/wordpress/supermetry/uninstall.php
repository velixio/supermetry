<?php
/**
 * Removes the plugin's options and cached proxy assets.
 *
 * Order meta (_supermetry_purchase_sent) is kept on purpose: it stops a
 * reinstalled plugin from sending old orders again.
 *
 * @package Supermetry
 */

if ( ! defined( 'WP_UNINSTALL_PLUGIN' ) ) {
	exit;
}

/**
 * Deletes this site's Supermetry data.
 */
function supermetry_uninstall_site() {
	global $wpdb;
	delete_option( 'supermetry_settings' );
	delete_option( 'supermetry_js_generation' );
	// phpcs:ignore WordPress.DB.DirectDatabaseQuery.DirectQuery,WordPress.DB.DirectDatabaseQuery.NoCaching -- Transients have no prefix delete API.
	$wpdb->query(
		$wpdb->prepare(
			"DELETE FROM {$wpdb->options} WHERE option_name LIKE %s OR option_name LIKE %s",
			$wpdb->esc_like( '_transient_supermetry_js_' ) . '%',
			$wpdb->esc_like( '_transient_timeout_supermetry_js_' ) . '%'
		)
	);
}

if ( is_multisite() ) {
	foreach ( get_sites(
		array(
			'fields' => 'ids',
			'number' => 0,
		)
	) as $supermetry_blog_id ) {
		switch_to_blog( (int) $supermetry_blog_id );
		supermetry_uninstall_site();
		restore_current_blog();
	}
} else {
	supermetry_uninstall_site();
}
