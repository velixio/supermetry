# frozen_string_literal: true

require_relative "test_helper"
require "logger"
require "stringio"

class TestHTTP < Minitest::Test
  def setup
    @servers = []
  end

  def teardown
    @servers.each(&:stop)
    Supermetry.reset!
  end

  def server(statuses = [])
    StubServer.new(statuses).tap { |s| @servers << s }
  end

  def client(srv, **opts)
    Supermetry::Client.new(site: "site_1", host: srv.base_url, url: "https://supermetry.com/",
                           retry_delay: 0.01, **opts)
  end

  def test_sync_track_sends_headers_and_json
    srv = server
    ok = client(srv, async: false).track(
      "Signup", url: "https://supermetry.com/signup?x=1", props: { plan: "pro", seats: 3 },
                revenue: { amount: "49.90", currency: "USD" }, profile_id: "user_1",
                user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)", ip: "203.0.113.7",
                referrer: "https://news.example.com/"
    )
    assert ok
    assert_equal 1, srv.recorded.size
    req = srv.recorded.first
    assert_equal "POST", req.method
    assert_equal "/api/public/v1/events", req.path
    assert_equal "application/json", req.headers["content-type"]
    assert_equal "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)", req.headers["user-agent"]
    assert_equal "203.0.113.7", req.headers["x-forwarded-for"]
    assert_equal "https://supermetry.com", req.headers["origin"]
    body = req.body
    assert_equal 1, body["v"]
    assert_equal "site_1", body["site"]
    assert_equal "Signup", body["name"]
    assert_equal "https://supermetry.com/signup?x=1", body["url"]
    assert_equal "https://news.example.com/", body["referrer"]
    assert_equal({ "plan" => "pro", "seats" => 3 }, body["props"])
    assert_equal({ "amount" => "49.90", "currency" => "USD" }, body["revenue"])
    assert_equal "user_1", body["profile_id"]
    assert_match(/\A[0-9a-f]{32}\z/, body["id"])
    assert_kind_of Integer, body["ts"]
  end

  def test_async_pageview_and_identify_then_flush
    srv = server
    c = client(srv)
    assert c.pageview
    assert c.identify("user_9", { plan: "pro" }, ip: "198.51.100.4", user_agent: "Mozilla/5.0 X")
    assert c.flush(5)
    reqs = srv.recorded
    assert_equal %w[pageview identify], reqs.map { |r| r.body["name"] }
    assert_equal "Supermetry-Ruby/0.1.0", reqs[0].headers["user-agent"]
    assert_equal "https://supermetry.com/", reqs[0].body["url"]
    assert_equal({ "plan" => "pro" }, reqs[1].body["traits"])
    assert_equal "user_9", reqs[1].body["profile_id"]
    assert_equal "198.51.100.4", reqs[1].headers["x-forwarded-for"]
    assert c.shutdown(5)
  end

  def test_retries_once_with_same_id
    srv = server([503])
    assert client(srv, async: false).track("x")
    reqs = srv.recorded
    assert_equal 2, reqs.size
    assert_equal reqs[0].body["id"], reqs[1].body["id"]
    assert_equal reqs[0].body["ts"], reqs[1].body["ts"]
  end

  def test_gives_up_after_one_retry_and_reports
    srv = server([429, 429, 429])
    errors = []
    log = StringIO.new
    c = client(srv, async: false, on_error: ->(e, payload) { errors << [e, payload] }, logger: Logger.new(log))
    refute c.track("x")
    assert_equal 2, srv.recorded.size
    assert_equal 429, errors.first[0].status
    assert_equal "x", errors.first[1][:name]
    assert_includes log.string, "HTTP 429"
  end

  def test_bad_request_not_retried
    srv = server([400])
    refute client(srv, async: false).track("x")
    assert_equal 1, srv.recorded.size
  end

  def test_network_error_never_raises_in_async_mode
    port = TCPServer.new("127.0.0.1", 0).then { |s| s.addr[1].tap { s.close } }
    errors = Queue.new
    c = Supermetry::Client.new(site: "s", host: "http://127.0.0.1:#{port}", url: "https://supermetry.com/",
                               retry_delay: 0.01, on_error: ->(e, _) { errors << e })
    assert c.track("x")
    assert c.flush(5)
    assert_kind_of SystemCallError, errors.pop
    c.shutdown
  end

  def test_queue_full_drops_events
    srv = server
    errors = []
    c = client(srv, max_queue_size: 0, on_error: ->(e, _) { errors << e })
    refute c.track("x")
    assert_kind_of Supermetry::QueueFullError, errors.first
    c.shutdown
  end

  def test_after_shutdown_sends_inline
    srv = server
    c = client(srv)
    c.shutdown
    assert c.track("late")
    assert_equal ["late"], srv.recorded.map { |r| r.body["name"] }
  end

  def test_module_level_default_client
    srv = server
    refute Supermetry.track("before configure")
    Supermetry.configure(site: "site_1", host: srv.base_url) do |config|
      config.url = "https://supermetry.com/"
      config.retry_delay = 0.01
    end
    assert Supermetry.track("Signup", props: { plan: "pro" })
    assert Supermetry.flush(5)
    assert_equal "Signup", srv.recorded.first.body["name"]
    assert Supermetry.shutdown
  end
end
