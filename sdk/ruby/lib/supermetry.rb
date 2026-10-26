# frozen_string_literal: true

require_relative "supermetry/version"

# Server-side SDK for Supermetry, self-hosted web analytics.
#
#   Supermetry.configure(site: "your-site-id", host: "https://analytics.supermetry.com")
#   Supermetry.track("Signup", url: "https://supermetry.com/signup",
#                    user_agent: request.user_agent, ip: request.remote_ip)
module Supermetry
  # Base class for Supermetry errors. Tracking methods never raise these into
  # the caller; they are passed to the logger and the +on_error+ hook.
  class Error < StandardError; end

  # The event failed local validation and was not sent.
  class InvalidEventError < Error; end

  # The async queue was full and the event was dropped.
  class QueueFullError < Error; end

  # The server answered with a non-success status.
  class HTTPError < Error
    attr_reader :status

    def initialize(message, status)
      super(message)
      @status = status
    end
  end

  # Keyword options accepted by Supermetry.configure.
  Configuration = Struct.new(
    :site, :host, :url, :user_agent, :async, :logger, :endpoint, :disabled, :on_error,
    :max_queue_size, :max_retries, :retry_delay, :timeout, :send_origin,
    keyword_init: true
  )

  @client = nil
  @lock = Mutex.new

  class << self
    # Creates the module-level default client. Accepts the same keyword
    # options as Client.new, and/or a block receiving a Configuration.
    # Replacing an existing default client shuts the old one down.
    #
    #   Supermetry.configure do |c|
    #     c.site = "your-site-id"
    #     c.host = "https://analytics.supermetry.com"
    #   end
    #
    # @return [Supermetry::Client]
    def configure(**options)
      config = Configuration.new(**options)
      yield config if block_given?
      kwargs = config.to_h.compact
      client = Client.new(**kwargs)
      old = @lock.synchronize do
        previous = @client
        @client = client
        previous
      end
      old&.shutdown(1)
      client
    end

    # The module-level default client, or nil before configure.
    def client
      @lock.synchronize { @client }
    end

    # Clears the default client (shutting it down first).
    def reset!
      old = @lock.synchronize do
        previous = @client
        @client = nil
        previous
      end
      old&.shutdown(1)
      nil
    end

    # See Client#track. Returns false when not configured.
    def track(...)
      client&.track(...) || false
    end

    # See Client#pageview. Returns false when not configured.
    def pageview(...)
      client&.pageview(...) || false
    end

    # See Client#identify. Returns false when not configured.
    def identify(...)
      client&.identify(...) || false
    end

    # See Client#flush. Returns true when not configured.
    def flush(timeout = nil)
      c = client
      c ? c.flush(timeout) : true
    end

    # See Client#shutdown. Returns true when not configured.
    def shutdown(timeout = 5)
      c = client
      c ? c.shutdown(timeout) : true
    end
  end
end

require_relative "supermetry/client"
