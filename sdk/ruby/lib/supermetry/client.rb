# frozen_string_literal: true

require "json"
require "net/http"
require "securerandom"
require "uri"

module Supermetry
  # Sends events to a Supermetry server.
  #
  # In async mode (the default) events are validated in the caller's thread,
  # queued in a bounded in-memory queue and sent by a background thread.
  # Tracking methods never raise: failures are reported to +logger+ and the
  # +on_error+ hook.
  class Client
    EVENTS_PATH = "/api/public/v1/events"
    DEFAULT_USER_AGENT = "Supermetry-Ruby/#{VERSION}"

    MAX_BODY_BYTES = 8 * 1024
    MAX_PROPS = 30
    MAX_KEY_BYTES = 300
    MAX_VALUE_BYTES = 2000
    MAX_URL_BYTES = 2000
    MAX_NAME_BYTES = 300
    MAX_PROFILE_ID_CHARS = 128
    MAX_TRAITS = 20
    MAX_TRAIT_KEY_CHARS = 64
    MAX_TRAIT_VALUE_CHARS = 256
    MAX_RETRY_AFTER = 5.0

    RETRYABLE_ERRORS = [IOError, SystemCallError, SocketError, Timeout::Error].freeze

    # A validated request ready to be sent.
    Request = Struct.new(:body, :headers, :payload)

    attr_reader :site, :endpoint

    # @param site [String] public site ID (required)
    # @param host [String] Supermetry base URL, e.g. "https://analytics.example.com" (required)
    # @param url [String, nil] default absolute page URL for events without one
    # @param user_agent [String, nil] UA sent when the visitor's is not forwarded
    # @param async [Boolean] send from a background thread (default) or inline
    # @param logger [Logger, nil] receives warnings about dropped/failed events
    # @param endpoint [String, nil] full endpoint override (default host + EVENTS_PATH)
    # @param disabled [Boolean] make every call a no-op
    # @param on_error [#call, nil] called with (error, payload_hash_or_nil) on failure
    # @param max_queue_size [Integer] events buffered in async mode before dropping
    # @param max_retries [Integer] retries on network errors, 429 and 5xx (same event id)
    # @param retry_delay [Float] seconds between retries when no Retry-After is sent
    # @param timeout [Float] open/read timeout in seconds
    # @param send_origin [Boolean] send an Origin header derived from the event URL
    def initialize(site:, host:, url: nil, user_agent: nil, async: true, logger: nil,
                   endpoint: nil, disabled: false, on_error: nil, max_queue_size: 1000,
                   max_retries: 1, retry_delay: 0.5, timeout: 5, send_origin: true)
      raise ArgumentError, "site is required" if site.nil? || site.to_s.strip.empty?

      @site = site.to_s
      @endpoint = endpoint || build_endpoint(host)
      @endpoint_uri = URI.parse(@endpoint)
      unless @endpoint_uri.is_a?(URI::HTTP) && @endpoint_uri.host && !@endpoint_uri.host.empty?
        raise ArgumentError, "host must be an absolute http(s) URL"
      end

      @default_url = url
      @user_agent = user_agent.nil? || user_agent.to_s.strip.empty? ? DEFAULT_USER_AGENT : user_agent.to_s
      @async = async
      @logger = logger
      @disabled = disabled
      @on_error = on_error
      @max_queue_size = max_queue_size
      @max_retries = max_retries
      @retry_delay = retry_delay
      @timeout = timeout
      @send_origin = send_origin

      @mutex = Mutex.new
      @idle = ConditionVariable.new
      @pending = 0
      @queue = nil
      @worker = nil
      @worker_pid = nil
      @shutdown = false
      @at_exit_registered = false
    end

    # Tracks a custom event (or "pageview").
    #
    # @return [Boolean] true when the event was sent (sync) or queued (async)
    def track(name, url: nil, props: nil, revenue: nil, profile_id: nil, user_agent: nil,
              ip: nil, referrer: nil, screen_width: nil)
      return true if @disabled

      deliver(name, url: url, props: props, revenue: revenue, profile_id: profile_id,
                    user_agent: user_agent, ip: ip, referrer: referrer, screen_width: screen_width)
    end

    # Tracks a page view. +url+ falls back to the client default.
    def pageview(url: nil, props: nil, profile_id: nil, user_agent: nil, ip: nil, referrer: nil,
                 screen_width: nil)
      track("pageview", url: url, props: props, profile_id: profile_id, user_agent: user_agent,
                        ip: ip, referrer: referrer, screen_width: screen_width)
    end

    # Links the visitor to +profile_id+ and stores +traits+ (string => string,
    # max 20) on the profile. Invalid traits are dropped locally.
    #
    # The client is shared across visitors, so it does not remember the
    # profile ID: pass +profile_id:+ on later events of that user.
    #
    # @param ctx [Hash] url:, user_agent:, ip:, referrer:
    def identify(profile_id, traits = {}, **ctx)
      return true if @disabled

      unknown = ctx.keys - %i[url user_agent ip referrer]
      unless unknown.empty?
        report(ArgumentError.new("unknown identify options: #{unknown.join(", ")}"), nil)
        return false
      end
      deliver("identify", profile_id: profile_id, traits: traits || {}, **ctx)
    end

    # Blocks until every queued event has been sent or +timeout+ seconds pass.
    #
    # @return [Boolean] true when the queue drained
    def flush(timeout = nil)
      deadline = timeout && monotonic + timeout
      @mutex.synchronize do
        while @pending.positive?
          remaining = deadline && deadline - monotonic
          return false if remaining && remaining <= 0

          @idle.wait(@mutex, remaining)
        end
      end
      true
    end

    # Flushes queued events and stops the background thread. Later calls
    # are sent inline.
    def shutdown(timeout = 5)
      drained = flush(timeout)
      worker = nil
      @mutex.synchronize do
        @shutdown = true
        worker = @worker
        @queue&.push(:stop) if worker&.alive?
        @worker = nil
      end
      worker&.join(timeout)
      drained
    end

    # Validates an event and builds the request. Raises InvalidEventError.
    # Public for testing and advanced use; tracking methods call it for you.
    def build_request(name, url: nil, props: nil, revenue: nil, profile_id: nil, user_agent: nil,
                      ip: nil, referrer: nil, screen_width: nil, traits: nil)
      name = name.to_s
      invalid!("name must be 1-300 bytes") if name.empty? || name.bytesize > MAX_NAME_BYTES
      invalid!("traits are only allowed on identify") if traits && name != "identify"
      invalid!("send identify events with #identify") if traits.nil? && name == "identify"

      page_url = (url || @default_url)&.to_s
      invalid!("url is required (per event or as the client default)") if page_url.nil? || page_url.empty?
      parsed = validate_url!(page_url)

      payload = {
        v: 1,
        id: SecureRandom.uuid.delete("-"),
        ts: (Time.now.to_f * 1000).to_i,
        site: @site,
        name: name,
        url: page_url
      }
      ref = referrer&.to_s
      payload[:referrer] = ref if ref && !ref.empty? && ref.bytesize <= MAX_VALUE_BYTES && ref.valid_encoding?

      unless screen_width.nil?
        unless screen_width.is_a?(Integer) && screen_width.between?(0, 32_767)
          invalid!("screen_width must be an integer 0-32767")
        end
        payload[:screen_width] = screen_width
      end

      clean_props = normalize_props(props)
      payload[:props] = clean_props unless clean_props.empty?

      if revenue && name != "pageview"
        clean_revenue = normalize_revenue(revenue)
        payload[:revenue] = clean_revenue if clean_revenue
      end

      unless profile_id.nil?
        pid = profile_id.to_s
        invalid!("profile_id must be 1-128 characters") if pid.empty? || pid.length > MAX_PROFILE_ID_CHARS
        payload[:profile_id] = pid
      end
      invalid!("identify requires a profile_id") if name == "identify" && payload[:profile_id].nil?

      if traits
        clean_traits = normalize_traits(traits)
        payload[:traits] = clean_traits unless clean_traits.empty?
      end

      body = JSON.generate(payload)
      invalid!("payload exceeds 8 KB") if body.bytesize > MAX_BODY_BYTES

      ua = user_agent.to_s.strip.empty? ? @user_agent : user_agent.to_s
      headers = {
        "Content-Type" => "application/json",
        "User-Agent" => header_value!(ua, "user_agent")
      }
      headers["X-Forwarded-For"] = header_value!(ip.to_s.strip, "ip") unless ip.to_s.strip.empty?
      headers["Origin"] = origin_for(parsed) if @send_origin
      Request.new(body, headers, payload)
    end

    private

    def deliver(name, **fields)
      request = build_request(name, **fields)
      if @async && !@shutdown
        enqueue(request)
      else
        send_request(request)
      end
    rescue StandardError => e
      report(e, nil)
      false
    end

    def enqueue(request)
      queued = @mutex.synchronize do
        ensure_worker
        next false if @queue.size >= @max_queue_size

        @pending += 1
        @queue.push(request)
        true
      end
      report(QueueFullError.new("queue full, event dropped"), request.payload) unless queued
      queued
    end

    # Called with @mutex held. Restarts the worker after fork.
    def ensure_worker
      return if @worker&.alive? && @worker_pid == Process.pid

      @queue = Queue.new
      @pending = 0
      @worker_pid = Process.pid
      queue = @queue
      @worker = Thread.new { run_worker(queue) }
      @worker.name = "supermetry-worker" if @worker.respond_to?(:name=)
      @worker.abort_on_exception = false
      @worker.report_on_exception = false
      return if @at_exit_registered

      @at_exit_registered = true
      at_exit { shutdown(@timeout) }
    end

    def run_worker(queue)
      loop do
        request = queue.pop
        break if request == :stop || request.nil?

        begin
          send_request(request)
        rescue Exception => e # rubocop:disable Lint/RescueException
          report(e, request.payload)
        ensure
          @mutex.synchronize do
            @pending -= 1 if @pending.positive?
            @idle.broadcast
          end
        end
      end
    end

    # Sends inline, retrying network errors, 429 and 5xx with the same
    # event id. Never raises.
    def send_request(request)
      attempts = 0
      loop do
        attempts += 1
        can_retry = attempts <= @max_retries
        begin
          response = post(request)
        rescue StandardError => e
          if retryable?(e) && can_retry
            sleep(@retry_delay)
            next
          end
          report(e, request.payload)
          return false
        end

        code = response.code.to_i
        return true if code.between?(200, 299)

        if (code == 429 || code >= 500) && can_retry
          sleep(retry_after(response))
          next
        end
        report(HTTPError.new("Supermetry answered HTTP #{code}", code), request.payload)
        return false
      end
    end

    def post(request)
      uri = @endpoint_uri
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = @timeout
      http.read_timeout = @timeout
      http.write_timeout = @timeout if http.respond_to?(:write_timeout=)
      post = Net::HTTP::Post.new(uri.request_uri)
      request.headers.each { |k, v| post[k] = v }
      post.body = request.body
      http.start { |conn| conn.request(post) }
    end

    def retry_after(response)
      header = response["Retry-After"]
      return @retry_delay unless header && header.strip.match?(/\A\d+\z/)

      [header.to_f, MAX_RETRY_AFTER].min
    end

    def retryable?(error)
      return true if RETRYABLE_ERRORS.any? { |klass| error.is_a?(klass) }

      defined?(OpenSSL::SSL::SSLError) && error.is_a?(OpenSSL::SSL::SSLError)
    end

    def report(error, payload)
      @logger&.warn("[supermetry] #{error.class}: #{error.message}")
      @on_error&.call(error, payload)
    rescue StandardError
      nil
    end

    def build_endpoint(host)
      raise ArgumentError, "host is required" if host.nil? || host.to_s.strip.empty?

      "#{host.to_s.strip.chomp("/")}#{EVENTS_PATH}"
    end

    def validate_url!(value)
      invalid!("url exceeds 2000 bytes") if value.bytesize > MAX_URL_BYTES
      parsed = begin
        URI.parse(value)
      rescue URI::InvalidURIError
        invalid!("url must be an absolute http(s) URL")
      end
      unless parsed.is_a?(URI::HTTP) && parsed.host && !parsed.host.empty?
        invalid!("url must be an absolute http(s) URL")
      end
      invalid!("url must not contain userinfo") if parsed.userinfo

      parsed
    end

    def normalize_props(props)
      return {} if props.nil?
      invalid!("props must be a Hash") unless props.is_a?(Hash)

      clean = {}
      props.each do |key, value|
        key = key.to_s
        next if key.empty? || key.bytesize > MAX_KEY_BYTES || !key.valid_encoding?

        value = case value
                when String then value
                when Symbol then value.to_s
                when Integer, true, false then value
                when Float then value.finite? ? value : next
                when Numeric then value.to_f.finite? ? value.to_f : next
                else next
                end
        size = value.is_a?(String) ? value.bytesize : value.to_s.bytesize
        next if size > MAX_VALUE_BYTES || (value.is_a?(String) && !value.valid_encoding?)

        clean[key] = value
      end
      clean.sort_by { |k, _| k }.first(MAX_PROPS).to_h
    end

    def normalize_traits(traits)
      invalid!("traits must be a Hash") unless traits.is_a?(Hash)

      clean = {}
      traits.each do |key, value|
        next if value.nil?

        key = key.to_s
        value = value.to_s
        next if key.empty? || key.length > MAX_TRAIT_KEY_CHARS || value.length > MAX_TRAIT_VALUE_CHARS
        next unless key.valid_encoding? && value.valid_encoding?

        clean[key] = value
      end
      clean.sort_by { |k, _| k }.first(MAX_TRAITS).to_h
    end

    # Returns a normalized revenue hash, or nil (with a warning) when invalid.
    # The event itself is still sent, matching the server, which drops only
    # invalid revenue.
    def normalize_revenue(revenue)
      unless revenue.is_a?(Hash)
        report(InvalidEventError.new("revenue must be a Hash with :amount and :currency; revenue dropped"), nil)
        return nil
      end
      amount = revenue[:amount] || revenue["amount"]
      currency = (revenue[:currency] || revenue["currency"]).to_s
      decimal = case amount
                when Integer then amount.to_s
                when Float
                  amount.finite? && amount.positive? ? format("%.6f", amount).sub(/0+\z/, "").chomp(".") : nil
                when String then amount.strip
                else
                  defined?(BigDecimal) && amount.is_a?(BigDecimal) ? amount.to_s("F").sub(/\.0\z/, "") : nil
                end
      unless decimal&.match?(/\A\d+(\.\d{1,6})?\z/) && decimal.match?(/[1-9]/)
        report(InvalidEventError.new("revenue amount must be positive with at most 6 decimals; revenue dropped"), nil)
        return nil
      end
      unless currency.empty? || currency.match?(/\A[A-Z]{3}\z/)
        report(InvalidEventError.new("revenue currency must be an uppercase ISO 4217 code; revenue dropped"), nil)
        return nil
      end
      result = { amount: decimal }
      result[:currency] = currency unless currency.empty?
      result
    end

    def origin_for(uri)
      default_port = uri.scheme == "https" ? 443 : 80
      port = uri.port && uri.port != default_port ? ":#{uri.port}" : ""
      "#{uri.scheme}://#{uri.host.downcase}#{port}"
    end

    def header_value!(value, field)
      invalid!("#{field} is not a valid header value") if value.match?(/[\r\n\0]/)

      value
    end

    def invalid!(message)
      raise InvalidEventError, message
    end

    def monotonic
      Process.clock_gettime(Process::CLOCK_MONOTONIC)
    end
  end
end
