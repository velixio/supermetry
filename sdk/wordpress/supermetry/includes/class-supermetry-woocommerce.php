<?php
/**
 * WooCommerce purchase tracking.
 *
 * @package Supermetry
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

/**
 * Sends one server-side Purchase event per order when its order-received page is shown.
 */
final class Supermetry_WooCommerce {

	const META_SENT = '_supermetry_purchase_sent';

	/**
	 * Hooks.
	 */
	public static function init() {
		add_action( 'woocommerce_thankyou', array( __CLASS__, 'thankyou' ), 20, 1 );
	}

	/**
	 * Tracks the order shown on the order-received page.
	 *
	 * @param int|string $order_id Order ID.
	 */
	public static function thankyou( $order_id ) {
		$settings = Supermetry_Settings::get();
		if ( empty( $settings['woocommerce'] ) || ! function_exists( 'wc_get_order' ) || ! Supermetry_Tracker::should_track() ) {
			return;
		}
		$order = wc_get_order( $order_id );
		if ( ! $order instanceof WC_Order || $order->has_status( array( 'failed', 'cancelled' ) ) || $order->get_meta( self::META_SENT ) ) {
			return;
		}
		// Mark first so a reload while the request runs cannot send it twice; the
		// stable event ID also lets the Supermetry server drop a duplicate.
		$order->update_meta_data( self::META_SENT, (string) time() );
		$order->save_meta_data();

		$payload = self::payload( $order );
		$sent    = Supermetry_Client::send_event( 'Purchase', $payload['url'], $payload['props'], $payload['options'] );
		if ( ! $sent ) {
			// Retry on the next view of the order-received page.
			$order->delete_meta_data( self::META_SENT );
			$order->save_meta_data();
		}
	}

	/**
	 * Builds the event for an order. The order key is left out of the URL.
	 *
	 * @param WC_Order $order Order.
	 * @return array{url:string, props:array<string, scalar>, options:array<string, mixed>}
	 */
	public static function payload( WC_Order $order ) {
		$url = strtok( $order->get_checkout_order_received_url(), '?#' );
		if ( ! is_string( $url ) || '' === $url ) {
			$url = home_url( '/' );
		}
		$props   = array(
			'order_id' => (string) $order->get_order_number(),
			'items'    => (int) $order->get_item_count(),
		);
		$coupons = $order->get_coupon_codes();
		if ( ! empty( $coupons ) ) {
			$props['coupon'] = substr( implode( ',', $coupons ), 0, 200 );
		}
		$amount = wc_format_decimal( $order->get_total(), wc_get_price_decimals() );
		/**
		 * Filters the Supermetry Purchase event properties for an order.
		 *
		 * @param array<string, scalar> $props Properties.
		 * @param WC_Order              $order Order.
		 */
		$props = (array) apply_filters( 'supermetry_woocommerce_purchase_props', $props, $order );
		return array(
			'url'     => $url,
			'props'   => $props,
			'options' => array(
				'id'      => Supermetry_Client::event_id( 'woocommerce-order-' . $order->get_id() ),
				'revenue' => array(
					'amount'   => (string) $amount,
					'currency' => strtoupper( (string) $order->get_currency() ),
				),
			),
		);
	}
}
