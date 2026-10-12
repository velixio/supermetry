# frozen_string_literal: true

require_relative "test_helper"

class TestPayload < Minitest::Test
  URL = "https://example.com/signup"

  def client(**opts)
    Supermetry::Client.new(site: "site_1", host: "https://analytics.example.com/", **opts)
  end

  def build(name = "Signup", **fields)
    client.build_request(name, url: URL, **fields)
  end

  def test_endpoint_from_host
    assert_equal "https://analytics.example.com/api/public/v1/events", client.endpoint
    assert_equal "https://x.example/custom",
                 client(endpoint: "https://x.example/custom").endpoint
  end

  def test_config_validation
    assert_raises(ArgumentError) { Supermetry::Client.new(site: "", host: "https://a.example") }
    assert_raises(ArgumentError) { Supermetry::Client.new(site: "s", host: nil) }
    assert_raises(ArgumentError) { Supermetry::Client.new(site: "s", host: "ftp://a.example") }
  end

  def test_payload_shape
    req = build(props: { plan: "pro", seats: 5, ratio: 0.5, trial: true, nothing: nil },
                revenue: { amount: "49.90", currency: "USD" }, profile_id: "user_1",
                referrer: "https://google.com/", screen_width: 1280)
    body = JSON.parse(req.body)
    assert_equal 1, body["v"]
    assert_match(/\A[0-9a-f]{32}\z/, body["id"])
    assert_in_delta Time.now.to_f * 1000, body["ts"], 10_000
    assert_equal "site_1", body["site"]
    assert_equal "Signup", body["name"]
    assert_equal URL, body["url"]
    assert_equal "https://google.com/", body["referrer"]
    assert_equal 1280, body["screen_width"]
    assert_equal({ "plan" => "pro", "ratio" => 0.5, "seats" => 5, "trial" => true }, body["props"])
    assert_equal({ "amount" => "49.90", "currency" => "USD" }, body["revenue"])
    assert_equal "user_1", body["profile_id"]
    refute body.key?("traits")
    assert_equal "Supermetry-Ruby/0.1.0", req.headers["User-Agent"]
    assert_equal "https://example.com", req.headers["Origin"]
    refute req.headers.key?("X-Forwarded-For")
  end

  def test_forwards_visitor_user_agent_and_ip
    req = build(user_agent: "Mozilla/5.0 (X11; Linux x86_64)", ip: "203.0.113.7")
    assert_equal "Mozilla/5.0 (X11; Linux x86_64)", req.headers["User-Agent"]
    assert_equal "203.0.113.7", req.headers["X-Forwarded-For"]
    assert_equal "Custom/1.0", client(user_agent: "Custom/1.0").build_request("x", url: URL).headers["User-Agent"]
  end

  def test_default_url_and_missing_url
    c = client(url: "https://example.com/")
    assert_equal "https://example.com/", JSON.parse(c.build_request("x").body)["url"]
    assert_raises(Supermetry::InvalidEventError) { client.build_request("x") }
  end

  def test_rejects_bad_urls
    ["/relative", "ftp://example.com/", "https://user:pw@example.com/", "mailto:a@example.com",
     "https://example.com/#{"a" * 2000}"].each do |url|
      assert_raises(Supermetry::InvalidEventError, url) { client.build_request("x", url: url) }
    end
  end

  def test_rejects_bad_name_and_profile_id
    assert_raises(Supermetry::InvalidEventError) { build("") }
    assert_raises(Supermetry::InvalidEventError) { build("x" * 301) }
    assert_raises(Supermetry::InvalidEventError) { build(profile_id: "") }
    assert_raises(Supermetry::InvalidEventError) { build(profile_id: "é" * 129) }
    build(profile_id: "é" * 128)
  end

  def test_traits_only_on_identify
    assert_raises(Supermetry::InvalidEventError) { build("Signup", profile_id: "u", traits: { "a" => "b" }) }
    assert_raises(Supermetry::InvalidEventError) { build("identify", profile_id: "u") }
    assert_raises(Supermetry::InvalidEventError) { build("identify", traits: {}) }
    body = JSON.parse(build("identify", profile_id: "u", traits: { plan: :pro, n: 3 }).body)
    assert_equal({ "n" => "3", "plan" => "pro" }, body["traits"])
  end

  def test_invalid_traits_dropped_and_capped
    traits = { "" => "x", "k" * 65 => "x", "long" => "v" * 257, "nil" => nil }
    25.times { |i| traits[format("t%02d", i)] = "ok" }
    body = JSON.parse(build("identify", profile_id: "u", traits: traits).body)
    assert_equal 20, body["traits"].size
    assert(body["traits"].keys.all? { |k| k.start_with?("t") })
  end

  def test_props_truncated_and_invalid_dropped
    props = { "" => "x", "k" * 301 => "x", "big" => "v" * 2001, "nan" => Float::NAN, "arr" => [1] }
    40.times { |i| props[format("p%02d", i)] = i }
    body = JSON.parse(build(props: props).body)
    assert_equal 30, body["props"].size
    assert_equal 0, body["props"]["p00"]
    assert body["props"].key?("p29")
    refute body["props"].key?("p30")
  end

  def test_revenue_normalization
    rev = ->(r, name = "buy") { JSON.parse(build(name, revenue: r).body)["revenue"] }
    assert_equal({ "amount" => "0.3" }, rev.call({ amount: 0.1 + 0.2 }))
    assert_equal({ "amount" => "100", "currency" => "EUR" }, rev.call({ "amount" => 100, "currency" => "EUR" }))
    assert_nil rev.call({ amount: "1.1234567", currency: "USD" })
    assert_nil rev.call({ amount: 0, currency: "USD" })
    assert_nil rev.call({ amount: -5.0, currency: "USD" })
    assert_nil rev.call({ amount: "1", currency: "usd" })
    assert_nil rev.call({ amount: "1", currency: "USD" }, "pageview")
  end

  def test_body_limit
    props = (0...10).to_h { |i| ["p#{i}", "v" * 1000] }
    assert_raises(Supermetry::InvalidEventError) { build(props: props) }
  end

  def test_header_injection_rejected
    assert_raises(Supermetry::InvalidEventError) { build(user_agent: "x\r\nEvil: 1") }
  end

  def test_tracking_never_raises_on_invalid_events
    errors = []
    c = client(async: false, on_error: ->(e, _) { errors << e })
    refute c.track("x") # no url
    refute c.identify("u", {}, bogus: 1)
    assert_equal 2, errors.size
    assert_kind_of Supermetry::InvalidEventError, errors.first
  end

  def test_disabled_is_noop
    assert client(disabled: true).track("x")
  end
end
